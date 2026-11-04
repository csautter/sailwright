import base64
import json
import logging
import os
import re
from functools import lru_cache
from pathlib import Path
from time import monotonic, sleep
from uuid import uuid4

import azure.functions as func
import requests
from azure.core.exceptions import (
    AzureError,
    HttpResponseError,
    ResourceNotFoundError,
    ServiceRequestError,
    ServiceResponseError,
)
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

# GitHub repo: only allow "owner/repo" with safe characters, no path traversal
_REPO_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._-]*$")

# Ownership marker applied to every broker-created resource group. delete_resource_group
# requires this tag as a defense-in-depth companion to the name-prefix guard.
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


@app.route(
    route="delete_resource_group",
    auth_level=func.AuthLevel.ANONYMOUS,
    methods=["POST"],
)
def delete_resource_group(req: func.HttpRequest) -> func.HttpResponse:
    authorized, reason = require_authenticated_caller(req)
    if not authorized:
        logging.warning("Unauthorized delete_resource_group request: %s", reason)
        return func.HttpResponse("Unauthorized", status_code=401)
    return handle_delete_resource_group(req)


def handle_delete_resource_group(req: func.HttpRequest) -> func.HttpResponse:
    logging.info("Delete resource group request received")
    try:
        credential = DefaultAzureCredential()
        subscription_id = os.environ["SUBSCRIPTION_ID"]
        # TODO: resource group name needs to follow a naming convention to avoid deleting unintended groups
        # TODO: create a check to validate the resource group is intended for deletion
        body = req.get_json()
        resource_group = body.get("resource-group", os.environ["RESOURCE_GROUP"])
        validate_source_group_name(resource_group)
        resource_client = ResourceManagementClient(credential, subscription_id)
        try:
            rg = resource_client.resource_groups.get(resource_group)
        except ResourceNotFoundError:
            # Idempotent: a retry after a deletion that already finished must
            # succeed. Nothing is deleted here, so no ownership check is needed.
            return _delete_response(resource_group, DELETE_STATUS_DELETED, 200)

        # Defense in depth: require the broker's ownership tag in addition to the
        # name-prefix check before deleting. A same-prefix RG that the broker did
        # not create (missing tag) is refused.
        validate_source_group_ownership(rg, resource_group)
        if (
            rg.properties and rg.properties.provisioning_state or ""
        ).lower() == "deleting":
            return _delete_response(resource_group, DELETE_STATUS_DELETING, 202)

        # Start the deletion without waiting for it: deleting a runner VM takes
        # longer than the ~230 s front-end request limit of the Function App, so
        # waiting turned successful deletions into 504 Gateway Time-out errors.
        # Callers poll this endpoint until it reports "deleted".
        resource_client.resource_groups.begin_delete(resource_group)
        return _delete_response(resource_group, DELETE_STATUS_DELETING, 202)
    except Exception as e:
        logging.error("Error deleting resource group: %s", e)
        return func.HttpResponse(
            "Internal error deleting resource group", status_code=500
        )


# Client transport limits also apply to requests made by ARM pollers. Key Vault
# retries are managed explicitly below. Disable SDK retries so Retry-After
# cannot keep an HTTP request asleep beyond the frontend deadline.
_SDK_OPTIONS = dict(
    connection_timeout=3,
    read_timeout=5,
    retry_total=0,
)
_NETWORK_WAIT_SECONDS = 20
_REQUEST_BUDGET_SECONDS = 180
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
)


@lru_cache(maxsize=1)
def _runner_config():
    values = {key: os.environ.get(key, "").strip() for key in _REQUIRED_SETTINGS}
    missing = sorted(key for key, value in values.items() if not value)
    return values, missing


class _BrokerFailure(Exception):
    def __init__(self, status, message, retry_after=None):
        self.status = status
        self.message = message
        self.retry_after = retry_after


def _resource_names(config):
    # A private VNet per request also avoids concurrent subnet updates. Keep
    # configured names as prefixes and use the strict common ARM character set.
    suffix = uuid4().hex
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
    correlation = getattr(req, "headers", {}).get("X-Correlation-ID", "")
    if not re.fullmatch(r"[a-zA-Z0-9._-]{1,64}", correlation):
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


def _arm_code(error):
    code = getattr(getattr(error, "error", None), "code", None)
    # ARM codes are identifiers. Discard arbitrary upstream text.
    return (
        code
        if isinstance(code, str) and re.fullmatch(r"[a-zA-Z0-9._/-]{1,128}", code)
        else None
    )


def _dependency_error(context, error):
    code = _arm_code(error)
    _log_stage(
        context,
        "failed",
        logging.ERROR,
        exception=type(error).__name__,
        **({"arm-code": code} if code else {}),
    )
    if isinstance(error, _BrokerFailure):
        return _runner_response(
            context, error.status, error.message, retry_after=error.retry_after
        )
    if isinstance(error, (TimeoutError, requests.Timeout)):
        return _runner_response(context, 504, "Dependency timed out")
    if isinstance(error, (ServiceRequestError, ServiceResponseError)):
        return _runner_response(
            context, 503, "Azure service unavailable", retry_after=5
        )
    if isinstance(error, HttpResponseError):
        status = getattr(error, "status_code", None)
        if status == 409:
            return _runner_response(context, 409, "Azure resource conflict")
        if status == 429 or status is not None and status >= 500:
            return _runner_response(
                context, 503, "Azure service unavailable", retry_after=5
            )
        return _runner_response(context, 502, "Azure request failed")
    if isinstance(error, (AzureError, requests.RequestException)):
        return _runner_response(context, 502, "Dependency request failed")
    return _runner_response(context, 500, "Internal error preparing runner")


def _network_result(poller, deadline):
    # Reserve time for the final VM submission (and its possible Spot fallback).
    wait_seconds = min(_NETWORK_WAIT_SECONDS, max(0, deadline - monotonic() - 20))
    result = poller.result(timeout=wait_seconds)
    # LROPoller.result(timeout=...) may return before completion without raising.
    if not poller.done():
        raise _BrokerFailure(
            504,
            "Network provisioning timed out; clean up the resource group before retrying",
        )
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
            if status < 500 or attempt == 2:
                raise _BrokerFailure(502, "GitHub registration request failed")
        _log_stage(context, "retry", logging.WARNING, attempt=attempt + 1)
        sleep(0.5 * 2**attempt)


@app.route(
    route="request_runner", auth_level=func.AuthLevel.ANONYMOUS, methods=["POST"]
)
def request_runner(req: func.HttpRequest) -> func.HttpResponse:
    context = _request_context(req)
    context["stage"] = "auth"
    authorized, reason = require_authenticated_caller(req)
    _log_stage(context, "authorized" if authorized else "rejected")
    if not authorized:
        if reason == "server misconfiguration":
            config, _ = _runner_config()
            missing = [
                key
                for key in ("ALLOWED_AUDIENCE", "ALLOWED_TENANT_ID")
                if not config[key]
            ]
            return _runner_response(
                context, 503, "Missing required app settings", missing=missing
            )
        return _runner_response(context, 401, "Unauthorized")
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
    try:
        return _provision_runner(req, context, config)
    except Exception as error:
        return _dependency_error(context, error)


def _provision_runner(req, context, config):
    deadline = monotonic() + _REQUEST_BUDGET_SECONDS
    context["stage"] = "input"
    try:
        body = req.get_json()
        repo = body["repo"]
        runner_name = body.get("runner-name", "gh-runner-vm")
        resource_group = body.get("resource-group", config["RESOURCE_GROUP"])
        virtualization_flavor = body.get("virtualization-flavor", "hyperv")
        if not isinstance(repo, str) or not _REPO_RE.fullmatch(repo):
            raise ValueError("repo")
        if (
            not isinstance(resource_group, str)
            or not re.fullmatch(r"gh-runner-tmp[a-zA-Z0-9_.()-]{0,77}", resource_group)
            or resource_group.endswith(".")
        ):
            raise ValueError("resource-group")
        if (
            not isinstance(runner_name, str)
            or not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9-]{0,63}", runner_name)
            or runner_name[:15].isdigit()
        ):
            raise ValueError("runner-name")
        if virtualization_flavor not in _ALLOWED_FLAVORS:
            raise ValueError("virtualization-flavor")
    except (ValueError, KeyError, TypeError, AttributeError):
        return _runner_response(
            context,
            400,
            "Invalid request body, repo, resource-group, runner-name or virtualization-flavor",
        )
    names = _resource_names(config)
    context.update({"resource-group": resource_group, "vm": names["vm"]})
    _log_stage(context, "validated", resources=names)

    context["stage"] = "pat"
    credential = DefaultAzureCredential(**_SDK_OPTIONS, process_timeout=5)
    kv_client = SecretClient(
        vault_url=config["VAULT_URL"],
        credential=credential,
        **_SDK_OPTIONS,
    )
    pat = _get_secret(kv_client, "github-runner-pat", context)
    _log_stage(context, "retrieved")
    context["stage"] = "github-token"
    runner_token = _github_token(pat, repo, context)
    _log_stage(context, "acquired")

    context["stage"] = "bootstrap"
    script = load_powershell_script(
        runner_token, f"https://github.com/{repo}", virtualization_flavor
    )
    custom_data = base64.b64encode(script.encode("utf-8")).decode("utf-8")
    context["stage"] = "admin-password"
    admin_password = _get_secret(kv_client, "github-runner-vm-admin-pw", context)
    _log_stage(context, "retrieved")

    context["stage"] = "resource-group"
    compute = ComputeManagementClient(
        credential, config["SUBSCRIPTION_ID"], **_SDK_OPTIONS
    )
    resource_client = ResourceManagementClient(
        credential, config["SUBSCRIPTION_ID"], **_SDK_OPTIONS
    )
    resource_client.resource_groups.create_or_update(
        resource_group,
        ResourceGroup(
            location=config["LOCATION"], tags={RG_OWNER_TAG: RG_OWNER_TAG_VALUE}
        ),
    )
    image_reference = compute_models.ImageReference(
        id=config["CUSTOM_IMAGE_ID"] + "-" + virtualization_flavor
    )

    context["stage"] = "network"
    # Create network resources
    network_client = NetworkManagementClient(
        credential, config["SUBSCRIPTION_ID"], polling_interval=1, **_SDK_OPTIONS
    )

    vnet_name = names["vnet"]
    subnet_name = os.environ.get("SUBNET_NAME", "default")
    ip_name = names["ip"]
    nic_name = names["nic"]
    location = config["LOCATION"]

    # Create VNet if not exists
    _network_result(
        network_client.virtual_networks.begin_create_or_update(
            resource_group,
            vnet_name,
            network_models.VirtualNetwork(
                location=location,
                address_space=network_models.AddressSpace(
                    address_prefixes=["10.0.0.0/16"]
                ),
            ),
        ),
        deadline,
    )

    # Create Subnet if not exists
    subnet = _network_result(
        network_client.subnets.begin_create_or_update(
            resource_group,
            vnet_name,
            subnet_name,
            network_models.Subnet(address_prefix="10.0.0.0/24"),
        ),
        deadline,
    )

    # Create Public IP
    public_ip = _network_result(
        network_client.public_ip_addresses.begin_create_or_update(
            resource_group,
            ip_name,
            network_models.PublicIPAddress(
                location=location,
                public_ip_allocation_method="Static",
                sku=network_models.PublicIPAddressSku(name="Standard"),
            ),
        ),
        deadline,
    )

    # Create NSG (if not exists).
    #
    # No inbound rules are added: the runner only needs OUTBOUND connectivity
    # to GitHub, and the default NSG rules deny all inbound traffic. RDP (3389)
    # was previously opened to `*` for first-run bootstrap debugging, which is
    # now proven; leaving a public IP with RDP open to the whole internet is an
    # unauthenticated attack surface (brute force, RDP CVEs). For the rare debug
    # session, use Azure Bastion / just-in-time VM access instead of a standing
    # inbound rule.
    nsg_name = names["nsg"]
    nsg = _network_result(
        network_client.network_security_groups.begin_create_or_update(
            resource_group,
            nsg_name,
            network_models.NetworkSecurityGroup(location=location),
        ),
        deadline,
    )

    # Create NIC and associate NSG
    nic = _network_result(
        network_client.network_interfaces.begin_create_or_update(
            resource_group,
            nic_name,
            network_models.NetworkInterface(
                location=location,
                ip_configurations=[
                    network_models.NetworkInterfaceIPConfiguration(
                        name="ipconfig1",
                        subnet=network_models.Subnet(id=subnet.id),
                        public_ip_address=network_models.PublicIPAddress(
                            id=public_ip.id
                        ),
                    )
                ],
                network_security_group=network_models.NetworkSecurityGroup(id=nsg.id),
            ),
        ),
        deadline,
    )

    _log_stage(context, "created")
    context["stage"] = "vm"
    # Keyword arguments shared by the regular and the spot VirtualMachine, so
    # the spot fallback sends none of the spot-only fields.
    vm_params = {
        "location": location,
        "hardware_profile": compute_models.HardwareProfile(vm_size=config["VM_SIZE"]),
        "storage_profile": compute_models.StorageProfile(
            image_reference=image_reference,
            os_disk=compute_models.OSDisk(
                create_option="FromImage",
                # disk size needs to be at minium 128GB - defined by the image size
                # disk_size_gb=int(os.environ.get("OS_DISK_SIZE_GB", "80")),
                managed_disk=compute_models.ManagedDiskParameters(
                    storage_account_type="Premium_LRS"
                ),
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

    # Configure spot VM if enabled
    use_spot = os.environ.get("VM_USE_SPOT", "false").lower() == "true"
    regular_vm = compute_models.VirtualMachine(**vm_params)
    if use_spot:
        # Built before the try below: a model error is a code bug and must not
        # be mistaken for missing spot capacity and fall back to a regular VM.
        spot_vm = compute_models.VirtualMachine(
            **vm_params,
            priority="Spot",
            eviction_policy="Deallocate",
            # -1 means pay up to on-demand price
            billing_profile=compute_models.BillingProfile(max_price=-1),
        )

        try:
            _log_stage(context, "spot-attempt")
            compute.virtual_machines.begin_create_or_update(
                resource_group, names["vm"], spot_vm, polling=False
            )
        except HttpResponseError as spot_error:
            _log_stage(
                context,
                "spot-fallback",
                logging.WARNING,
                exception=type(spot_error).__name__,
                **(
                    {"arm-code": _arm_code(spot_error)} if _arm_code(spot_error) else {}
                ),
            )
            # Retry without the spot configuration
            compute.virtual_machines.begin_create_or_update(
                resource_group, names["vm"], regular_vm, polling=False
            )
    else:
        compute.virtual_machines.begin_create_or_update(
            resource_group, names["vm"], regular_vm, polling=False
        )
    _log_stage(context, "submitted")
    return _runner_response(
        context,
        202,
        "Runner VM creation started",
        resources=names,
        status="provisioning",
        **{"status-url": "/api/runner_status"},
    )


@app.route(route="runner_status", auth_level=func.AuthLevel.ANONYMOUS, methods=["POST"])
def runner_status(req: func.HttpRequest) -> func.HttpResponse:
    context = _request_context(req)
    context["stage"] = "auth"
    authorized, reason = require_authenticated_caller(req)
    _log_stage(context, "authorized" if authorized else "rejected")
    if not authorized:
        if reason == "server misconfiguration":
            config, _ = _runner_config()
            missing = [
                key
                for key in ("ALLOWED_AUDIENCE", "ALLOWED_TENANT_ID")
                if not config[key]
            ]
            return _runner_response(
                context, 503, "Missing required app settings", missing=missing
            )
        return _runner_response(context, 401, "Unauthorized")
    return handle_runner_status(req, context)


def handle_runner_status(req: func.HttpRequest, context=None) -> func.HttpResponse:
    context = context or _request_context(req)
    context["stage"] = "input"
    try:
        body = req.get_json()
        resource_group = body["resource-group"]
        vm_name = body["vm"]
        if (
            not isinstance(resource_group, str)
            or not re.fullmatch(r"gh-runner-tmp[a-zA-Z0-9_.()-]{0,77}", resource_group)
            or resource_group.endswith(".")
        ):
            raise ValueError("resource-group")
        if not isinstance(vm_name, str) or not re.fullmatch(
            r"[a-zA-Z0-9][a-zA-Z0-9-]{0,63}", vm_name
        ):
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
        credential = DefaultAzureCredential(**_SDK_OPTIONS, process_timeout=5)
        context["stage"] = "resource-group"
        resources = ResourceManagementClient(
            credential, config["SUBSCRIPTION_ID"], **_SDK_OPTIONS
        )
        group = resources.resource_groups.get(resource_group)
        if (group.tags or {}).get(RG_OWNER_TAG) != RG_OWNER_TAG_VALUE:
            _log_stage(context, "ownership-rejected", logging.WARNING)
            return _runner_response(
                context, 403, "Resource group is not owned by the runner broker"
            )
        context["stage"] = "vm-status"
        compute = ComputeManagementClient(
            credential, config["SUBSCRIPTION_ID"], **_SDK_OPTIONS
        )
        vm = compute.virtual_machines.get(
            resource_group, vm_name, expand="instanceView"
        )
        state = (vm.provisioning_state or "unknown").lower()
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
        _log_stage(context, "observed", status=status)
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


def validate_source_group_name(name: str) -> bool:
    if not name.startswith("gh-runner-tmp"):
        raise Exception("Invalid source group name")

    return True


def _delete_response(
    resource_group: str, status: str, status_code: int
) -> func.HttpResponse:
    if status == DELETE_STATUS_DELETED:
        message = f"Resource group '{resource_group}' deleted."
    else:
        message = f"Deletion of resource group '{resource_group}' in progress."
    return func.HttpResponse(
        json.dumps(
            {"message": message, "status": status, "resource-group": resource_group}
        ),
        mimetype="application/json",
        status_code=status_code,
    )


def validate_source_group_ownership(rg, name: str) -> bool:
    """Confirm the resource group carries the broker's ownership tag.

    Refuses deletion if the RG was not created by this broker (missing/incorrect
    ``managed-by`` tag), even when its name matches the prefix.
    """
    tags = rg.tags or {}
    if tags.get(RG_OWNER_TAG) != RG_OWNER_TAG_VALUE:
        raise Exception(
            f"Resource group '{name}' is not owned by gh-runner-broker; refusing to delete"
        )

    return True
