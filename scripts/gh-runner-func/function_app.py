import base64
import hashlib
import json
import logging
import os
import re
import traceback
from contextlib import contextmanager
from email.utils import formatdate
from functools import lru_cache
from pathlib import Path
from time import monotonic, sleep, time
from uuid import uuid4

import azure.functions as func
import requests
from azure.core import MatchConditions
from azure.core.exceptions import (
    AzureError,
    HttpResponseError,
    ResourceNotFoundError,
    ServiceRequestError,
    ServiceResponseError,
)
from azure.core.pipeline.policies import RetryPolicy
from azure.identity import DefaultAzureCredential
from azure.keyvault.secrets import SecretClient
from azure.mgmt.compute import ComputeManagementClient
from azure.mgmt.compute import models as compute_models
from azure.mgmt.network import NetworkManagementClient
from azure.mgmt.network import models as network_models
from azure.mgmt.resource.resources import ResourceManagementClient
from azure.mgmt.resource.resources.models import ResourceGroup

# Request bodies are built from SDK model objects, never plain dicts: since
# azure-mgmt-network 31 and azure-mgmt-compute 38 the SDKs send dict bodies to
# ARM verbatim, so snake_case keys are no longer mapped to the camelCase
# "properties" payload and ARM rejects the request (InvalidRequestContent).

app = func.FunctionApp()

# Allowed virtualization flavors — must match the image naming convention
_ALLOWED_FLAVORS: frozenset[str] = frozenset({"hyperv", "virtualbox"})

# GitHub repo: allow names such as .github while rejecting URL dot segments.
_REPO_RE = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9._-]+")

# Broker resource groups: the gh-runner-tmp prefix plus the ARM resource group
# character set, up to 90 characters and not ending in a period.
_RESOURCE_GROUP_RE = re.compile(r"gh-runner-tmp[a-zA-Z0-9_.()-]{0,77}")

# Runner names and generated VM names: letters, digits and hyphens.
_NAME_RE = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9-]{0,63}")

# Idempotency-Key request header for request_runner. A retry with the same key
# targets the same resource names instead of creating a second runner VM.
_IDEMPOTENCY_KEY_RE = re.compile(r"[a-zA-Z0-9._:-]{1,128}")

# VM tag holding a fingerprint of the request that created it. Only set for
# requests with an Idempotency-Key, to detect a key reused with another body.
VM_REQUEST_TAG = "broker-request"

# Ownership marker applied to every broker-created resource group. delete_resource_group
# and runner_status require this tag as a defense-in-depth companion to the name-prefix
# guard, and request_runner refuses to adopt an existing group that lacks it.
RG_OWNER_TAG = "managed-by"
RG_OWNER_TAG_VALUE = "gh-runner-broker"

# delete_resource_group response states. "deleting" means the caller should poll
# again; "deleted" means the resource group no longer exists.
DELETE_STATUS_DELETING = "deleting"
DELETE_STATUS_DELETED = "deleted"


# EasyAuth v2 serialises Azure AD claim types as full URI strings.
# Map them to their short JWT equivalents so the rest of the code
# can use simple names regardless of token version.
_CLAIM_URI_TO_SHORT: dict[str, str] = {
    "http://schemas.microsoft.com/identity/claims/tenantid": "tid",
    "http://schemas.microsoft.com/identity/claims/objectidentifier": "oid",
    "http://schemas.microsoft.com/identity/claims/identityprovider": "idp",
    "http://schemas.microsoft.com/claims/authnmethodsreferences": "amr",
    "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/nameidentifier": "sub",
    "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress": "email",
    "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name": "name",
}

_AUTH_SETTINGS = ("ALLOWED_AUDIENCE", "ALLOWED_TENANT_ID")


def _get_claims_from_principal_header(req: func.HttpRequest) -> dict[str, str]:
    principal_b64 = req.headers.get("X-MS-CLIENT-PRINCIPAL")
    if not principal_b64:
        raise ValueError("missing principal header")
    decoded = base64.b64decode(principal_b64)
    principal = json.loads(decoded)
    claims: dict[str, str] = {}
    for claim in principal.get("claims", []):
        claim_type = claim.get("typ")
        claim_value = claim.get("val")
        if claim_type and claim_value:
            # Normalise full URI claim types to their short JWT equivalents
            short_claim_type: str = _CLAIM_URI_TO_SHORT.get(
                claim_type, claim_type or ""
            )
            claims[short_claim_type] = claim_value
    return claims


def _csv_env(name: str) -> set[str]:
    raw = os.environ.get(name, "")
    return {value.strip() for value in raw.split(",") if value.strip()}


def require_authenticated_caller(req: func.HttpRequest) -> tuple[bool, str]:
    try:
        claims = _get_claims_from_principal_header(req)
    except Exception as err:
        return False, f"invalid principal: {err}"

    allowed_tenant = os.environ.get("ALLOWED_TENANT_ID", "")
    allowed_audience = os.environ.get("ALLOWED_AUDIENCE", "")
    if not allowed_tenant or not allowed_audience:
        logging.error("ALLOWED_TENANT_ID or ALLOWED_AUDIENCE is not configured")
        return False, "server misconfiguration"

    allowed_client_ids = _csv_env("ALLOWED_CLIENT_IDS")
    allowed_user_object_ids = _csv_env("ALLOWED_USER_OBJECT_IDS")

    tenant_id = claims.get("tid", "")
    audience = claims.get("aud", "")
    client_id = claims.get("azp") or claims.get("appid") or ""
    user_object_id = claims.get("oid", "")

    if tenant_id != allowed_tenant:
        return False, "tenant not allowed"
    if audience != allowed_audience:
        return False, "audience not allowed"

    client_allowed = client_id in allowed_client_ids
    user_allowed = user_object_id in allowed_user_object_ids
    if not client_allowed and not user_allowed:
        return False, "caller not allowlisted"

    return True, ""


def load_powershell_script(
    runner_token: str, repo_url: str, virtualization_flavor: str
) -> str:
    """Load and prepare the PowerShell runner setup script."""
    script_path = Path(__file__).parent / "runner-setup.ps1"
    with open(script_path, "r", encoding="utf-8") as f:
        script = f.read()

    # Replace placeholders
    script = script.replace("__RUNNER_TOKEN__", runner_token)
    script = script.replace("__REPO_URL__", repo_url)
    script = script.replace("__VIRTUALIZATION_FLAVOR__", virtualization_flavor)

    return script


# --- Dependency clients -------------------------------------------------------

# Seconds a server Retry-After may delay one SDK retry. ARM can ask for much
# longer waits, which would keep the HTTP request asleep past the frontend limit.
_SDK_RETRY_AFTER_MAX = 2


class _BoundedRetryPolicy(RetryPolicy):
    """azure-core retries whose server Retry-After is capped."""

    def get_retry_after(self, response):
        retry_after = super().get_retry_after(response)
        return None if retry_after is None else min(retry_after, _SDK_RETRY_AFTER_MAX)


def _arm_options():
    # ARM PUTs are idempotent for a fixed resource name, so SDK retries after a
    # read timeout resend the same request instead of creating new resources.
    # Each call stops starting new attempts after 15 s. A fresh policy per
    # client: pipeline policies are linked to their pipeline and cannot be shared.
    return dict(
        connection_timeout=3,
        read_timeout=10,
        retry_policy=_BoundedRetryPolicy(
            retry_total=2,
            retry_backoff_factor=0.5,
            retry_backoff_max=2,
            timeout=15,
        ),
    )


# Key Vault retries are managed explicitly by _get_secret.
_KEY_VAULT_OPTIONS = dict(connection_timeout=3, read_timeout=5, retry_total=0)

# Managed identity token requests are cheap and occasionally flaky: keep a
# short bounded retry.
_CREDENTIAL_OPTIONS = dict(
    connection_timeout=3,
    read_timeout=5,
    retry_total=2,
    retry_backoff_factor=0.5,
    retry_backoff_max=2,
    process_timeout=5,
)


def _credential():
    return DefaultAzureCredential(**_CREDENTIAL_OPTIONS)


# --- Request budget -------------------------------------------------------------
#
# The Function App frontend aborts HTTP requests after about 230 s. Everything up
# to the VM submission must finish within _REQUEST_BUDGET_SECONDS. Resource
# creation only starts with at least _MIN_PROVISION_SECONDS left, and network
# waits leave time for the VM submission (and its Spot fallback).
_REQUEST_BUDGET_SECONDS = 160
_MIN_PROVISION_SECONDS = 60
_VM_RESERVE_SECONDS = 30
# Three GitHub attempts, each with 3 s connect and 5 s read timeouts, plus backoff.
_GITHUB_TOKEN_RESERVE_SECONDS = 26
_NETWORK_WAIT_SECONDS = 20
_LEASE_SECONDS = 60
# A stalled ARM call can outlive its blob lease. Leave enough time for the
# request budget, one final dependency call and clock skew between workers.
_IN_PROGRESS_SECONDS = _REQUEST_BUDGET_SECONDS + _LEASE_SECONDS + 10

_REQUIRED_SETTINGS = (
    "ALLOWED_TENANT_ID",
    "ALLOWED_AUDIENCE",
    "VAULT_URL",
    "SUBSCRIPTION_ID",
    "LOCATION",
    "RESOURCE_GROUP",
    "VM_NAME",
    "VM_SIZE",
    "ADMIN_USERNAME",
    "CUSTOM_IMAGE_ID",
    "ALLOWED_REPOS",
)


def _valid_storage_url(value, account):
    # The deployment uses the public Azure Blob endpoint for this account.
    return bool(
        re.fullmatch(r"[a-z0-9]{3,24}", account)
        and re.fullmatch(
            rf"(?ai:https://{account}\.blob\.core\.windows\.net)(?::443)?/[a-z0-9-]{{3,63}}/?",
            value,
        )
    )


@lru_cache(maxsize=1)
def _runner_config():
    values = {key: os.environ.get(key, "").strip() for key in _REQUIRED_SETTINGS}
    repos = {repo.strip() for repo in values["ALLOWED_REPOS"].split(",") if repo.strip()}
    values["ALLOWED_REPOS"] = (
        {repo.lower() for repo in repos} if all(_valid_repo(repo) for repo in repos) else set()
    )
    missing = sorted(key for key, value in values.items() if not value)
    storage_url = os.environ.get("IDEMPOTENCY_STORAGE_URL", "").strip()
    storage_account = os.environ.get("IDEMPOTENCY_STORAGE_ACCOUNT", "").strip()
    values["IDEMPOTENCY_STORAGE_URL"] = (
        storage_url if _valid_storage_url(storage_url, storage_account) else ""
    )
    return values, missing


class _BrokerFailure(Exception):
    def __init__(self, status, message, retry_after=None):
        super().__init__(message)
        self.status = status
        self.message = message
        self.retry_after = retry_after


# --- Shared request helpers -----------------------------------------------------


def _valid_resource_group(name):
    return (
        isinstance(name, str)
        and _RESOURCE_GROUP_RE.fullmatch(name) is not None
        and not name.endswith(".")
    )


def _owned_by_broker(group):
    return (getattr(group, "tags", None) or {}).get(RG_OWNER_TAG) == RG_OWNER_TAG_VALUE


def _group_state(group):
    properties = getattr(group, "properties", None)
    return ((properties and properties.provisioning_state) or "").lower()


def _valid_repo(repo):
    return (
        isinstance(repo, str)
        and _REPO_RE.fullmatch(repo) is not None
        and not repo.endswith(("/.", "/.."))
    )


def _idempotency_key(req):
    """Return the Idempotency-Key header, None when absent, or raise ValueError."""
    key = (getattr(req, "headers", None) or {}).get("Idempotency-Key")
    if key is None:
        return None
    if not isinstance(key, str) or not _IDEMPOTENCY_KEY_RE.fullmatch(key):
        raise ValueError("Idempotency-Key")
    return key


def _request_fingerprint(repo, runner_name, resource_group, virtualization_flavor):
    payload = json.dumps(
        [repo.lower(), runner_name, resource_group, virtualization_flavor]
    )
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


class _IdempotencyRecord:
    """An Azure blob binds one key to a request and leases its provisioning slot."""

    def __init__(self, credential, storage_url, key):
        self.credential = credential
        self.url = storage_url.rstrip("/") + "/" + hashlib.sha256(key.encode()).hexdigest()
        self.lease_id = str(uuid4())

    def _request(self, method, headers=None, **kwargs):
        try:
            token = self.credential.get_token("https://storage.azure.com/.default")
            return requests.request(
                method,
                self.url,
                headers={
                    "Authorization": f"Bearer {token.token}",
                    "x-ms-date": formatdate(usegmt=True),
                    "x-ms-version": "2023-11-03",
                    **(headers or {}),
                },
                timeout=(3, 5),
                allow_redirects=False,
                **kwargs,
            )
        except (AzureError, requests.RequestException) as error:
            raise self._unavailable() from error

    @staticmethod
    def _unavailable():
        return _BrokerFailure(503, "Idempotency storage is unavailable", 5)

    @staticmethod
    def in_progress():
        return _BrokerFailure(503, "A request with this Idempotency-Key is in progress", 5)

    @staticmethod
    def _parse_record(response):
        try:
            record = response.json()
        except (ValueError, TypeError):
            raise _BrokerFailure(502, "Idempotency storage returned an invalid record") from None
        resources = record.get("resources") if isinstance(record, dict) else None
        if (
            not isinstance(record, dict)
            or not isinstance(record.get("fingerprint"), str)
            or not re.fullmatch(r"[0-9a-f]{64}", record["fingerprint"])
            or (
                # Legacy records have neither field. A partial binding is invalid.
                ("resource_group" in record or "resource_group_defaulted" in record)
                and (
                    not _valid_resource_group(record.get("resource_group"))
                    or not isinstance(record.get("resource_group_defaulted"), bool)
                )
            )
            or not isinstance(record.get("subnet"), str)
            or not record["subnet"]
            or not isinstance(record.get("accepted"), bool)
            or type(record.get("busy_until")) is not int
            or record["busy_until"] < 0
            or not isinstance(resources, dict)
            or any(
                not isinstance(resources.get(key), str)
                or not re.fullmatch(r"[a-z0-9][a-z0-9-]{0,79}", resources[key])
                for key in ("vnet", "ip", "nic", "nsg")
            )
            or not isinstance(resources.get("vm"), str)
            or not _NAME_RE.fullmatch(resources["vm"])
        ):
            raise _BrokerFailure(502, "Idempotency storage returned an invalid record")
        return record

    def reserve(self, initial):
        response = self._request("GET")
        if response.status_code == 200:
            return self._parse_record(response)
        if response.status_code != 404:
            raise self._unavailable()
        response = self._request(
            "PUT",
            headers={"If-None-Match": "*", "x-ms-blob-type": "BlockBlob"},
            json=initial,
        )
        if response.status_code == 201:
            return initial
        if response.status_code in (409, 412):
            return self.read()
        raise self._unavailable()

    def read(self):
        response = self._request("GET")
        if response.status_code != 200:
            raise self._unavailable()
        return self._parse_record(response)

    def _lease_action(self, action, **headers):
        return self._request(
            "PUT", {"x-ms-lease-action": action, **headers}, params={"comp": "lease"}
        )

    @contextmanager
    def lease(self, context):
        response = self._lease_action(
            "acquire",
            **{
                "x-ms-proposed-lease-id": self.lease_id,
                "x-ms-lease-duration": str(_LEASE_SECONDS),
            },
        )
        error_code = next(
            (v for k, v in (response.headers or {}).items() if k.lower() == "x-ms-error-code"),
            None,
        )
        if response.status_code == 409 and error_code in (
            "LeaseAlreadyPresent", "LeaseIsBreakingAndCannotBeAcquired"
        ):
            raise self.in_progress()
        if response.status_code != 201:
            raise self._unavailable()
        try:
            yield
        finally:
            try:
                if self._lease_action("release", **{"x-ms-lease-id": self.lease_id}).status_code != 200:
                    _log_stage(context, "idempotency-lease-release-failed", logging.WARNING)
            except _BrokerFailure:
                _log_stage(context, "idempotency-lease-release-failed", logging.WARNING)

    def renew(self):
        response = self._lease_action("renew", **{"x-ms-lease-id": self.lease_id})
        if response.status_code != 200:
            raise self._unavailable()

    def write(self, record, **changes):
        response = self._request(
            "PUT",
            headers={"x-ms-blob-type": "BlockBlob", "x-ms-lease-id": self.lease_id},
            json={**record, **changes},
        )
        if response.status_code != 201:
            raise self._unavailable()

    def accept(self, record):
        self.write(record, accepted=True, busy_until=0)


def _resource_names(config, idempotency_key=None):
    # A private VNet per request also avoids concurrent subnet updates. Keep
    # configured names as prefixes and use the strict common ARM character set.
    # With an Idempotency-Key the suffix is derived from the key, so a retry
    # PUTs the same names (ARM PUTs to an existing name are idempotent).
    if idempotency_key is None:
        suffix = uuid4().hex
    else:
        suffix = hashlib.sha256(idempotency_key.encode("utf-8")).hexdigest()[:32]
    names = {}
    for key, default, limit in (
        ("vm", config["VM_NAME"], 64),
        ("vnet", os.environ.get("VNET_NAME", "gh-runner-vnet"), 64),
        ("ip", os.environ.get("IP_NAME", "gh-runner-ip"), 80),
        ("nic", os.environ.get("NIC_NAME", "gh-runner-nic"), 80),
        ("nsg", os.environ.get("NSG_NAME", "gh-runner-nsg"), 80),
    ):
        prefix = re.sub(r"[^a-z0-9-]", "-", default.lower()).strip("-") or "runner"
        names[key] = f"{prefix[: limit - len(suffix) - 1]}-{suffix}"
    return names


def _request_context(req):
    correlation = (getattr(req, "headers", None) or {}).get("X-Correlation-ID", "")
    if not isinstance(correlation, str) or not re.fullmatch(
        r"[a-zA-Z0-9._-]{1,64}", correlation
    ):
        correlation = uuid4().hex
    return {"correlation-id": correlation, "stage": "input"}


def _log_stage(context, event, level=logging.INFO, **fields):
    # Never stringify exceptions or log upstream bodies: either can contain
    # credentials, VM customData or the Authorization header.
    logging.log(level, json.dumps({**context, "event": event, **fields}))


def _runner_response(context, status_code, message, **fields):
    retry_after = fields.pop("retry_after", None)
    headers = {"X-Correlation-ID": context["correlation-id"]}
    if retry_after is not None:
        headers["Retry-After"] = str(retry_after)
    return func.HttpResponse(
        json.dumps({"message": message, **context, **fields}),
        status_code=status_code,
        mimetype="application/json",
        headers=headers,
    )


def _authorize(req, context):
    """Return an error response for a rejected caller, or None to proceed."""
    context["stage"] = "auth"
    authorized, reason = require_authenticated_caller(req)
    _log_stage(context, "authorized" if authorized else "rejected")
    if authorized:
        return None
    if reason == "server misconfiguration":
        # The caller is not authorized yet: setting names go to the log only.
        missing = [key for key in _AUTH_SETTINGS if not os.environ.get(key)]
        _log_stage(context, "missing-settings", logging.ERROR, missing=missing)
        return _runner_response(context, 503, "Server misconfiguration")
    return _runner_response(context, 401, "Unauthorized")


def _arm_code(error):
    code = getattr(getattr(error, "error", None), "code", None)
    # ARM codes are identifiers. Discard arbitrary upstream text.
    return (
        code
        if isinstance(code, str) and re.fullmatch(r"[a-zA-Z0-9._/-]{1,128}", code)
        else None
    )


def _error_fields(error):
    fields = {"exception": type(error).__name__}
    code = _arm_code(error)
    if code:
        fields["arm-code"] = code
    upstream_status = getattr(error, "status_code", None)
    if isinstance(error, HttpResponseError) and isinstance(upstream_status, int):
        fields["upstream-status"] = upstream_status
    return fields


def _frames(error):
    # File, line and function only: exception messages may carry secrets.
    return [
        f"{Path(frame.filename).name}:{frame.lineno}:{frame.name}"
        for frame in traceback.extract_tb(error.__traceback__)
    ][-10:]


def _classify(error):
    """Map an exception to (HTTP status, message, Retry-After seconds)."""
    if isinstance(error, _BrokerFailure):
        return error.status, error.message, error.retry_after
    if isinstance(error, (TimeoutError, requests.Timeout)):
        return 504, "Dependency timed out", None
    if isinstance(error, (ServiceRequestError, ServiceResponseError)):
        return 503, "Azure service unavailable", 5
    if isinstance(error, HttpResponseError):
        status = getattr(error, "status_code", None)
        if status == 409:
            return 409, "Azure resource conflict", None
        if status == 429 or status is not None and status >= 500:
            return 503, "Azure service unavailable", 5
        return 502, "Azure request failed", None
    if isinstance(error, (AzureError, requests.RequestException)):
        return 502, "Dependency request failed", None
    return 500, "Internal error", None


def _dependency_error(context, error, resources=None, idempotent=False):
    """Build the error response for a failed request.

    ``resources`` is set once request_runner has started writing to Azure. From
    then on, resources may exist even when the request failed (an ARM write can
    succeed after a client timeout), so the response asks for cleanup and never
    carries Retry-After. Without an Idempotency-Key, request_runner is not
    idempotent; with one, a retry with the same key reuses the same resources.
    """
    status, message, retry_after = _classify(error)
    fields = _error_fields(error)
    if status == 500:
        fields["frames"] = _frames(error)
    _log_stage(context, "failed", logging.ERROR, **fields)
    extra = {}
    if resources is not None:
        retry_after = None
        extra = {"cleanup-required": True, "resources": resources}
        if idempotent:
            message = (
                f"{message}; resources may exist, retry with the same Idempotency-Key "
                "or clean up the resource group"
            )
        else:
            message = f"{message}; resources may exist, clean up the resource group before retrying"
    return _runner_response(context, status, message, retry_after=retry_after, **extra)


# --- delete_resource_group ------------------------------------------------------


@app.route(
    route="delete_resource_group",
    auth_level=func.AuthLevel.ANONYMOUS,
    methods=["POST"],
)
def delete_resource_group(req: func.HttpRequest) -> func.HttpResponse:
    context = _request_context(req)
    denied = _authorize(req, context)
    if denied is not None:
        return denied
    return handle_delete_resource_group(req, context)


def _delete_response(context, status, status_code):
    resource_group = context["resource-group"]
    if status == DELETE_STATUS_DELETED:
        message = f"Resource group '{resource_group}' deleted."
    else:
        message = f"Deletion of resource group '{resource_group}' in progress."
    return _runner_response(context, status_code, message, status=status)


def handle_delete_resource_group(
    req: func.HttpRequest, context=None
) -> func.HttpResponse:
    context = context or _request_context(req)
    context["stage"] = "input"
    try:
        body = req.get_json()
        resource_group = body.get(
            "resource-group", os.environ.get("RESOURCE_GROUP", "")
        )
        if not _valid_resource_group(resource_group):
            raise ValueError("resource-group")
    except (ValueError, TypeError, AttributeError):
        return _runner_response(
            context, 400, "Invalid request body or resource-group"
        )
    context["resource-group"] = resource_group
    subscription_id = os.environ.get("SUBSCRIPTION_ID", "").strip()
    if not subscription_id:
        return _runner_response(
            context, 503, "Missing required app settings", missing=["SUBSCRIPTION_ID"]
        )
    try:
        context["stage"] = "resource-group"
        resource_client = ResourceManagementClient(
            _credential(), subscription_id, **_arm_options()
        )
        try:
            group = resource_client.resource_groups.get(resource_group)
        except ResourceNotFoundError:
            # Idempotent: a retry after a deletion that already finished must
            # succeed. Nothing is deleted here, so no ownership check is needed.
            return _delete_response(context, DELETE_STATUS_DELETED, 200)

        # Defense in depth: require the broker's ownership tag in addition to the
        # name-prefix check before deleting. A same-prefix RG that the broker did
        # not create (missing tag) is refused.
        if not _owned_by_broker(group):
            _log_stage(context, "ownership-rejected", logging.WARNING)
            return _runner_response(
                context, 403, "Resource group is not owned by the runner broker"
            )
        if _group_state(group) == "deleting":
            return _delete_response(context, DELETE_STATUS_DELETING, 202)

        # Start the deletion without waiting for it: deleting a runner VM takes
        # longer than the ~230 s front-end request limit of the Function App, so
        # waiting turned successful deletions into 504 Gateway Time-out errors.
        # Callers poll this endpoint until it reports "deleted".
        context["stage"] = "delete"
        resource_client.resource_groups.begin_delete(resource_group, polling=False)
        _log_stage(context, "delete-started")
        return _delete_response(context, DELETE_STATUS_DELETING, 202)
    except Exception as error:
        return _dependency_error(context, error)


# --- request_runner -------------------------------------------------------------


def _network_result(poller, deadline):
    # Reserve time for the final VM submission (and its possible Spot fallback).
    wait_seconds = min(
        _NETWORK_WAIT_SECONDS, max(0, deadline - monotonic() - _VM_RESERVE_SECONDS)
    )
    result = poller.result(timeout=wait_seconds)
    # LROPoller.result(timeout=...) may return before completion without raising.
    if not poller.done():
        raise _BrokerFailure(504, "Network provisioning timed out")
    return result


def _get_secret(client, name, context):
    for attempt in range(3):
        try:
            value = client.get_secret(name).value
            if not isinstance(value, str) or not value:
                raise _BrokerFailure(502, "Key Vault returned an empty secret")
            return value
        except (ServiceRequestError, ServiceResponseError, HttpResponseError) as error:
            status = getattr(error, "status_code", None)
            transient = not isinstance(error, HttpResponseError) or status in (
                408,
                429,
                500,
                502,
                503,
                504,
            )
            if not transient or attempt == 2:
                raise
            _log_stage(context, "retry", logging.WARNING, attempt=attempt + 1)
            sleep(0.5 * 2**attempt)


def _github_token(pat, repo, context):
    url = f"https://api.github.com/repos/{repo}/actions/runners/registration-token"
    for attempt in range(3):
        try:
            response = requests.post(
                url,
                headers={
                    "Authorization": f"Bearer {pat}",
                    "Accept": "application/vnd.github+json",
                    "X-GitHub-Api-Version": "2022-11-28",
                },
                timeout=(3, 5),
                allow_redirects=False,
            )
        except requests.RequestException:
            if attempt == 2:
                raise
        else:
            status = response.status_code
            _log_stage(context, "response", upstream_status=status)
            if status == 201:
                try:
                    token = response.json()["token"]
                    if not isinstance(token, str) or not token:
                        raise ValueError("empty token")
                    return token
                except (ValueError, KeyError, TypeError):
                    raise _BrokerFailure(
                        502, "GitHub returned an invalid registration token"
                    ) from None
            if status in (401, 403):
                raise _BrokerFailure(502, "GitHub authentication/authorization failed")
            if status == 404:
                raise _BrokerFailure(400, "GitHub repository not found or inaccessible")
            if status == 429:
                retry = response.headers.get("Retry-After", "60")
                retry = (
                    int(retry)
                    if isinstance(retry, str) and retry.isdigit() and len(retry) < 7
                    else 60
                )
                raise _BrokerFailure(
                    503, "GitHub rate limit exceeded; retry later", max(1, retry)
                )
            if 300 <= status < 400:
                # Redirects are not followed so the PAT never leaves api.github.com.
                raise _BrokerFailure(
                    502,
                    "GitHub redirected the request; use the repository's current owner/name",
                )
            if status < 500 or attempt == 2:
                raise _BrokerFailure(502, "GitHub registration request failed")
        _log_stage(context, "retry", logging.WARNING, attempt=attempt + 1)
        sleep(0.5 * 2**attempt)


def _registration_data(pat, repo, virtualization_flavor, context):
    context["stage"] = "github-token"
    runner_token = _github_token(pat, repo, context)
    _log_stage(context, "acquired")

    context["stage"] = "bootstrap"
    script = load_powershell_script(
        runner_token, f"https://github.com/{repo}", virtualization_flavor
    )
    return base64.b64encode(script.encode("utf-8")).decode("utf-8")


@app.route(
    route="request_runner", auth_level=func.AuthLevel.ANONYMOUS, methods=["POST"]
)
def request_runner(req: func.HttpRequest) -> func.HttpResponse:
    context = _request_context(req)
    denied = _authorize(req, context)
    if denied is not None:
        return denied
    return handle_request_runner(req, context)


def handle_request_runner(req: func.HttpRequest, context=None) -> func.HttpResponse:
    context = context or _request_context(req)
    context["stage"] = "config"
    config, missing = _runner_config()
    if missing:
        _log_stage(context, "missing-settings", logging.ERROR, missing=missing)
        return _runner_response(
            context, 503, "Missing required app settings", missing=missing
        )
    # Filled in once the first Azure write starts; see _dependency_error.
    progress = {}
    try:
        return _provision_runner(req, context, config, progress)
    except Exception as error:
        return _dependency_error(
            context,
            error,
            progress.get("resources"),
            idempotent=progress.get("idempotent", False),
        )


def _parse_runner_request(req, config):
    """Return the validated request fields and whether the group was omitted."""
    body = req.get_json()
    repo = body["repo"]
    runner_name = body.get("runner-name", "gh-runner-vm")
    group_defaulted = "resource-group" not in body
    resource_group = body.get("resource-group", config["RESOURCE_GROUP"])
    virtualization_flavor = body.get("virtualization-flavor", "hyperv")
    if not _valid_repo(repo):
        raise ValueError("repo")
    if not _valid_resource_group(resource_group):
        raise ValueError("resource-group")
    if (
        not isinstance(runner_name, str)
        or not _NAME_RE.fullmatch(runner_name)
        or runner_name[:15].isdigit()
    ):
        raise ValueError("runner-name")
    if virtualization_flavor not in _ALLOWED_FLAVORS:
        raise ValueError("virtualization-flavor")
    return (repo, runner_name, resource_group, virtualization_flavor), group_defaulted


def _match_runner_request(record, runner_request, group_defaulted):
    repo, runner_name, resource_group, flavor = runner_request
    # Legacy fingerprints can only match the resolved group, since they do
    # not reveal the original group or whether the caller omitted it.
    bound_group = record.get("resource_group", resource_group)
    if group_defaulted and record.get("resource_group_defaulted"):
        resource_group = bound_group
    matched_request = (repo, runner_name, resource_group, flavor)
    if (
        resource_group != bound_group
        or _request_fingerprint(*matched_request) != record["fingerprint"]
    ):
        raise _BrokerFailure(409, "Idempotency-Key was already used for a different request")
    return matched_request


def _existing_runner(resource_client, compute, resource_group, vm_name, fingerprint):
    """Return True when a previous request with this Idempotency-Key created the VM.

    Raises _BrokerFailure(409) when the key was used with a different request
    body or the group is not the broker's. Nothing is written here.
    """
    try:
        group = resource_client.resource_groups.get(resource_group)
    except ResourceNotFoundError:
        return False
    if not _owned_by_broker(group):
        raise _BrokerFailure(
            409, "Resource group exists and is not owned by the runner broker"
        )
    if _group_state(group) == "deleting":
        raise _BrokerFailure(
            409, "Resource group is being deleted; use a new group or wait for deletion"
        )
    try:
        vm = compute.virtual_machines.get(resource_group, vm_name)
    except ResourceNotFoundError:
        return False
    if (getattr(vm, "tags", None) or {}).get(VM_REQUEST_TAG) != fingerprint:
        raise _BrokerFailure(
            409, "Idempotency-Key was already used for a different request"
        )
    return True


def _accepted_response(context, names, replayed=False):
    fields = {}
    if replayed:
        fields["replayed"] = True
    return _runner_response(
        context,
        202,
        "Runner VM creation started",
        resources=names,
        status="provisioning",
        **{"status-url": "/api/runner_status"},
        **fields,
    )


def _ensure_resource_group(resource_client, resource_group):
    """Create the group, or reuse one the broker already owns.

    Never adopt a foreign group: stamping the ownership tag on it would let
    delete_resource_group delete it later, and a PUT would replace its tags.
    """
    try:
        group = resource_client.resource_groups.get(resource_group)
    except ResourceNotFoundError:
        group = None
    if group is None:
        return False
    if not _owned_by_broker(group):
        raise _BrokerFailure(
            409, "Resource group exists and is not owned by the runner broker"
        )
    if _group_state(group) == "deleting":
        raise _BrokerFailure(
            409, "Resource group is being deleted; use a new group or wait for deletion"
        )
    return True


def _network_resource(operations, path, model, deadline, keyed, before_write):
    if keyed:
        # In particular, avoid re-PUTting a working VNet without its subnet
        # in the body. A partial attempt may already have attached its NIC.
        try:
            existing = operations.get(*path)
        except ResourceNotFoundError:
            pass
        else:
            state = (getattr(existing, "provisioning_state", None) or "").lower()
            if state == "succeeded":
                return existing
            if state not in ("failed", "canceled", "cancelled"):
                raise _BrokerFailure(503, "An Azure network operation is still pending", 5)
    before_write()
    return _network_result(operations.begin_create_or_update(*path, model), deadline)


def _create_network(
    network_client, resource_group, names, location, deadline, subnet_name, keyed, before_write
):
    """Create VNet, subnet, public IP, NSG and NIC; return the NIC."""
    vnet_name = names["vnet"]
    _network_resource(
        network_client.virtual_networks,
        (resource_group, vnet_name),
        network_models.VirtualNetwork(
            location=location,
            address_space=network_models.AddressSpace(address_prefixes=["10.0.0.0/16"]),
        ),
        deadline, keyed, before_write,
    )

    subnet = _network_resource(
        network_client.subnets,
        (resource_group, vnet_name, subnet_name),
        network_models.Subnet(address_prefix="10.0.0.0/24"),
        deadline, keyed, before_write,
    )

    public_ip = _network_resource(
        network_client.public_ip_addresses,
        (resource_group, names["ip"]),
        network_models.PublicIPAddress(
            location=location,
            public_ip_allocation_method="Static",
            sku=network_models.PublicIPAddressSku(name="Standard"),
        ),
        deadline, keyed, before_write,
    )

    # No inbound rules are added: the runner only needs OUTBOUND connectivity
    # to GitHub, and the default NSG rules deny all inbound traffic. RDP (3389)
    # was previously opened to `*` for first-run bootstrap debugging, which is
    # now proven; leaving a public IP with RDP open to the whole internet is an
    # unauthenticated attack surface (brute force, RDP CVEs). For the rare debug
    # session, use Azure Bastion / just-in-time VM access instead of a standing
    # inbound rule.
    nsg = _network_resource(
        network_client.network_security_groups,
        (resource_group, names["nsg"]),
        network_models.NetworkSecurityGroup(location=location),
        deadline, keyed, before_write,
    )

    return _network_resource(
        network_client.network_interfaces,
        (resource_group, names["nic"]),
        network_models.NetworkInterface(
            location=location,
            ip_configurations=[
                network_models.NetworkInterfaceIPConfiguration(
                    name="ipconfig1",
                    subnet=network_models.Subnet(id=subnet.id),
                    public_ip_address=network_models.PublicIPAddress(id=public_ip.id),
                )
            ],
            network_security_group=network_models.NetworkSecurityGroup(id=nsg.id),
        ),
        deadline, keyed, before_write,
    )


def _submit_vm(compute, resource_group, vm_name, vm_params, context, keyed, before_write):
    """Submit the VM without waiting; try Spot first when VM_USE_SPOT=true."""
    options = {"polling": False}
    if keyed:
        # A timed-out PUT can still reach Compute after a subsequent GET saw
        # no VM. Prevent either request from updating a VM the other created.
        options["match_condition"] = MatchConditions.IfMissing
    regular_vm = compute_models.VirtualMachine(**vm_params)
    if os.environ.get("VM_USE_SPOT", "false").lower() == "true":
        # Built before the try below: a model error is a code bug and must not
        # be mistaken for missing spot capacity and fall back to a regular VM.
        spot_vm = compute_models.VirtualMachine(
            **vm_params,
            priority="Spot",
            eviction_policy="Deallocate",
            # -1 means pay up to on-demand price
            billing_profile=compute_models.BillingProfile(max_price=-1),
        )
        _log_stage(context, "spot-attempt")
        before_write()
        try:
            compute.virtual_machines.begin_create_or_update(
                resource_group, vm_name, spot_vm, **options
            )
            return
        except HttpResponseError as spot_error:
            if keyed and (
                spot_error.status_code not in (400, 409)
                or _arm_code(spot_error) not in (
                    "SkuNotAvailable",
                    "AllocationFailed",
                    "ZonalAllocationFailed",
                    "AzureSpotFeatureNotEnabledForSubscription",
                    "AzureSpotIsNotSupportedForThisVMSize",
                )
            ):
                raise
            _log_stage(
                context, "spot-fallback", logging.WARNING, **_error_fields(spot_error)
            )
    before_write()
    compute.virtual_machines.begin_create_or_update(
        resource_group, vm_name, regular_vm, **options
    )


def _provision_runner(req, context, config, progress):
    deadline = monotonic() + _REQUEST_BUDGET_SECONDS
    context["stage"] = "input"
    try:
        idempotency_key = _idempotency_key(req)
    except ValueError:
        return _runner_response(
            context, 400, "Invalid Idempotency-Key header: use 1-128 of [A-Za-z0-9._:-]"
        )
    try:
        runner_request, group_defaulted = _parse_runner_request(req, config)
    except (ValueError, KeyError, TypeError, AttributeError):
        return _runner_response(
            context,
            400,
            "Invalid request body, repo, resource-group, runner-name or virtualization-flavor",
        )
    repo, _, resource_group, _ = runner_request
    if repo.lower() not in config["ALLOWED_REPOS"]:
        # The GitHub PAT may reach more repositories than the broker serves.
        _log_stage(context, "repo-rejected", logging.WARNING)
        return _runner_response(context, 403, "Repository is not allowed")
    names = _resource_names(config, idempotency_key)
    context["resource-group"] = resource_group
    if idempotency_key is None:
        _log_stage(context, "validated", resources=names, idempotent=False)

    subnet_name = os.environ.get("SUBNET_NAME") or "default"
    storage_url = config["IDEMPOTENCY_STORAGE_URL"]
    if idempotency_key is not None and not storage_url:
        return _runner_response(
            context, 503, "IDEMPOTENCY_STORAGE_URL must match IDEMPOTENCY_STORAGE_ACCOUNT",
            missing=["IDEMPOTENCY_STORAGE_URL"],
        )
    credential = _credential()
    if idempotency_key is None:
        context["vm"] = names["vm"]
        _create_runner(credential, config, runner_request, names, subnet_name,
                       deadline, context, progress)
        return _accepted_response(context, names)

    context["stage"] = "idempotency"
    progress["idempotent"] = True
    reservation = _IdempotencyRecord(credential, storage_url, idempotency_key)
    record = reservation.reserve({
        "fingerprint": _request_fingerprint(*runner_request),
        "resource_group": resource_group,
        "resource_group_defaulted": group_defaulted,
        "resources": names,
        "subnet": subnet_name,
        "accepted": False,
        "busy_until": 0,
    })
    bound_request = _match_runner_request(record, runner_request, group_defaulted)
    names = record["resources"]
    context.update({"resource-group": bound_request[2], "vm": names["vm"]})
    _log_stage({**context, "stage": "input"}, "validated", resources=names, idempotent=True)
    if record["accepted"]:
        _log_stage(context, "replayed")
        return _accepted_response(context, names, replayed=True)

    with reservation.lease(context):
        # Another worker may have finished or the storage policy may have
        # expired an old record between the read and the lease acquisition.
        record = reservation.read()
        bound_request = _match_runner_request(record, runner_request, group_defaulted)
        resource_group = bound_request[2]
        fingerprint = record["fingerprint"]
        names = record["resources"]
        context.update({"resource-group": resource_group, "vm": names["vm"]})
        if record["accepted"]:
            _log_stage(context, "replayed")
            return _accepted_response(context, names, replayed=True)
        resource_client = ResourceManagementClient(
            credential, config["SUBSCRIPTION_ID"], **_arm_options()
        )
        compute = ComputeManagementClient(
            credential, config["SUBSCRIPTION_ID"], **_arm_options()
        )
        # ARM may have accepted a VM before a previous worker timed out.
        # Recover it with reads even while the provisioning claim is active.
        replayed = _existing_runner(
            resource_client, compute, resource_group, names["vm"], fingerprint
        )
        if not replayed:
            now = time()
            if record["busy_until"] > now:
                raise reservation.in_progress()
            # Refresh Last-Modified before provisioning. Lease operations alone
            # do not extend blob retention or guard an uncertain ARM write.
            reservation.write(record, busy_until=int(now) + _IN_PROGRESS_SECONDS)
            try:
                _create_runner(
                    credential, config, bound_request, names, record["subnet"],
                    deadline, context, progress, reservation, fingerprint,
                )
            except Exception as error:
                if not progress.get("arm_write_pending"):
                    try:
                        reservation.write(record, busy_until=0)
                    except _BrokerFailure:
                        _log_stage(context, "idempotency-claim-clear-failed", logging.WARNING)
                # A delayed first submission can win the create-only PUT.
                if (
                    context["stage"] != "vm" or not isinstance(error, HttpResponseError)
                    or error.status_code != 412
                    or not _existing_runner(
                        resource_client, compute, resource_group, names["vm"], fingerprint
                    )
                ):
                    raise
                replayed = True
        # The VM exists or was submitted. Preserve the pending claim if this
        # write is uncertain so we never overwrite an accepted record.
        progress["resources"] = names
        context["stage"] = "idempotency"
        reservation.accept(record)
        if replayed:
            _log_stage(context, "replayed")
        return _accepted_response(context, names, replayed=replayed)


def _create_runner(
    credential, config, runner_request, names, subnet_name, deadline, context, progress,
    reservation=None, fingerprint=None,
):
    repo, runner_name, resource_group, virtualization_flavor = runner_request
    keyed = reservation is not None

    def before_write():
        if keyed:
            reservation.renew()
            if monotonic() >= deadline:
                raise _BrokerFailure(504, "Request budget exhausted before an Azure write", 5)
            progress["arm_write_pending"] = True
        progress["resources"] = names

    context["stage"] = "pat"
    kv_client = SecretClient(
        vault_url=config["VAULT_URL"], credential=credential, **_KEY_VAULT_OPTIONS
    )
    pat = _get_secret(kv_client, "github-runner-pat", context)
    _log_stage(context, "retrieved")
    if not keyed:
        custom_data = _registration_data(pat, repo, virtualization_flavor, context)
    context["stage"] = "admin-password"
    admin_password = _get_secret(kv_client, "github-runner-vm-admin-pw", context)
    _log_stage(context, "retrieved")

    context["stage"] = "resource-group"
    if deadline - monotonic() < _MIN_PROVISION_SECONDS:
        # Nothing has been written yet, so retrying is safe.
        raise _BrokerFailure(
            504, "Request budget exhausted before provisioning started", 5
        )
    location = config["LOCATION"]
    resource_client = ResourceManagementClient(
        credential, config["SUBSCRIPTION_ID"], **_arm_options()
    )
    reused = _ensure_resource_group(resource_client, resource_group)
    if not reused:
        before_write()
        resource_client.resource_groups.create_or_update(
            resource_group,
            ResourceGroup(location=location, tags={RG_OWNER_TAG: RG_OWNER_TAG_VALUE}),
        )
        progress["arm_write_pending"] = False
    _log_stage(context, "ready", reused=reused)

    context["stage"] = "network"
    network_client = NetworkManagementClient(
        credential, config["SUBSCRIPTION_ID"], polling_interval=1, **_arm_options()
    )
    network_deadline = deadline - _GITHUB_TOKEN_RESERVE_SECONDS if keyed else deadline
    nic = _create_network(
        network_client, resource_group, names, location, network_deadline,
        subnet_name, keyed, before_write,
    )
    progress["arm_write_pending"] = False
    _log_stage(context, "created")

    if keyed:
        # A pending network operation must finish before requesting a token.
        context["stage"] = "github-token"
        reservation.renew()
        if deadline - monotonic() < _GITHUB_TOKEN_RESERVE_SECONDS + _VM_RESERVE_SECONDS:
            raise _BrokerFailure(504, "Request budget exhausted before getting a runner token", 5)
        custom_data = _registration_data(pat, repo, virtualization_flavor, context)

    context["stage"] = "vm"
    compute = ComputeManagementClient(
        credential, config["SUBSCRIPTION_ID"], **_arm_options()
    )
    # Keyword arguments shared by the regular and the spot VirtualMachine, so
    # the spot fallback sends none of the spot-only fields.
    disk_name = {"name": f"{names['vm']}-os"} if keyed else {}
    vm_params = {
        "location": location,
        "hardware_profile": compute_models.HardwareProfile(vm_size=config["VM_SIZE"]),
        "storage_profile": compute_models.StorageProfile(
            image_reference=compute_models.ImageReference(
                id=config["CUSTOM_IMAGE_ID"] + "-" + virtualization_flavor
            ),
            os_disk=compute_models.OSDisk(
                create_option="FromImage",
                # disk size needs to be at minium 128GB - defined by the image size
                # disk_size_gb=int(os.environ.get("OS_DISK_SIZE_GB", "80")),
                managed_disk=compute_models.ManagedDiskParameters(
                    storage_account_type="Premium_LRS"
                ),
                **disk_name,
            ),
        ),
        "os_profile": compute_models.OSProfile(
            # Windows computer names must be ≤15 chars
            computer_name=runner_name[:15],
            admin_username=config["ADMIN_USERNAME"],
            admin_password=admin_password,
            custom_data=custom_data,
        ),
        "network_profile": compute_models.NetworkProfile(
            network_interfaces=[compute_models.NetworkInterfaceReference(id=nic.id)]
        ),
    }
    if fingerprint is not None:
        vm_params["tags"] = {VM_REQUEST_TAG: fingerprint}
    _submit_vm(compute, resource_group, names["vm"], vm_params, context, keyed, before_write)
    _log_stage(context, "submitted")


# --- runner_status --------------------------------------------------------------


@app.route(route="runner_status", auth_level=func.AuthLevel.ANONYMOUS, methods=["POST"])
def runner_status(req: func.HttpRequest) -> func.HttpResponse:
    context = _request_context(req)
    denied = _authorize(req, context)
    if denied is not None:
        return denied
    return handle_runner_status(req, context)


def handle_runner_status(req: func.HttpRequest, context=None) -> func.HttpResponse:
    context = context or _request_context(req)
    context["stage"] = "input"
    try:
        body = req.get_json()
        resource_group = body["resource-group"]
        vm_name = body["vm"]
        if not _valid_resource_group(resource_group):
            raise ValueError("resource-group")
        if not isinstance(vm_name, str) or not _NAME_RE.fullmatch(vm_name):
            raise ValueError("vm")
    except (ValueError, KeyError, TypeError, AttributeError):
        return _runner_response(
            context, 400, "Expected resource-group and vm from the creation response"
        )
    context.update({"resource-group": resource_group, "vm": vm_name})
    context["stage"] = "config"
    config, _ = _runner_config()
    if not config["SUBSCRIPTION_ID"]:
        return _runner_response(
            context, 503, "Missing required app settings", missing=["SUBSCRIPTION_ID"]
        )
    try:
        credential = _credential()
        context["stage"] = "resource-group"
        resources = ResourceManagementClient(
            credential, config["SUBSCRIPTION_ID"], **_arm_options()
        )
        group = resources.resource_groups.get(resource_group)
        if not _owned_by_broker(group):
            _log_stage(context, "ownership-rejected", logging.WARNING)
            return _runner_response(
                context, 403, "Resource group is not owned by the runner broker"
            )
        if _group_state(group) == "deleting":
            return _deleting_status(context)
        context["stage"] = "vm-status"
        compute = ComputeManagementClient(
            credential, config["SUBSCRIPTION_ID"], **_arm_options()
        )
        vm = compute.virtual_machines.get(
            resource_group, vm_name, expand="instanceView"
        )
        state = (vm.provisioning_state or "unknown").lower()
        if state == "deleting":
            return _deleting_status(context)
        statuses = vm.instance_view.statuses if vm.instance_view else []
        # A VM can be evicted after provisioning succeeds. Neither a stopped VM
        # nor a deallocated one can register a runner and pick up CI work.
        failed_codes = [
            status.code
            for status in (statuses or [])
            if status.code
            and (
                status.code.lower().startswith("provisioningstate/failed")
                or state == "succeeded"
                and status.code.lower()
                in ("powerstate/deallocated", "powerstate/stopped")
            )
        ]
        if state in ("failed", "canceled", "cancelled") or failed_codes:
            code = failed_codes[0] if failed_codes else f"ProvisioningState/{state}"
            _log_stage(
                context, "provisioning-failed", logging.ERROR, **{"arm-code": code}
            )
            return _runner_response(
                context,
                200,
                "Runner VM provisioning failed or the VM stopped; clean up the resource group",
                status="failed",
                **{"error-code": code},
            )
        status = "succeeded" if state == "succeeded" else "provisioning"
        # Callers poll every 10 s; only state changes deserve INFO.
        _log_stage(
            context,
            "observed",
            logging.INFO if status == "succeeded" else logging.DEBUG,
            status=status,
        )
        return _runner_response(
            context,
            200 if status == "succeeded" else 202,
            "Runner VM provisioning status",
            status=status,
            retry_after=10 if status == "provisioning" else None,
        )
    except ResourceNotFoundError:
        _log_stage(context, "not-found", logging.WARNING)
        return _runner_response(
            context, 404, "Runner resource group or VM not found", status="not-found"
        )
    except Exception as error:
        return _dependency_error(context, error)


def _deleting_status(context):
    _log_stage(context, "deleting")
    return _runner_response(
        context,
        200,
        "Runner resource group or VM is being deleted; stop waiting",
        status="deleting",
    )
