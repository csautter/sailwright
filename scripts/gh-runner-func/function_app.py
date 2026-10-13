import base64
import azure.functions as func
import json
import logging
import re
import requests
import os
from pathlib import Path

from azure.identity import DefaultAzureCredential
from azure.keyvault.secrets import SecretClient
from azure.mgmt.compute import ComputeManagementClient
from azure.mgmt.resource import ResourceManagementClient
from azure.mgmt.network import NetworkManagementClient

app = func.FunctionApp()

# Allowed virtualization flavors — must match the image naming convention
_ALLOWED_FLAVORS: frozenset[str] = frozenset({"hyperv", "virtualbox"})

# GitHub repo: only allow "owner/repo" with safe characters, no path traversal
_REPO_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._-]*$")

# Ownership marker applied to every broker-created resource group. delete_resource_group
# requires this tag as a defense-in-depth companion to the name-prefix guard.
RG_OWNER_TAG = "managed-by"
RG_OWNER_TAG_VALUE = "gh-runner-broker"


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
        # Defense in depth: require the broker's ownership tag in addition to the
        # name-prefix check before deleting. A same-prefix RG that the broker did
        # not create (missing tag, or nonexistent) is refused.
        validate_source_group_ownership(resource_client, resource_group)
        # Delete the resource group and all resources within
        delete_async_op = resource_client.resource_groups.begin_delete(resource_group)
        delete_async_op.wait()
        return func.HttpResponse(
            json.dumps({"message": f"Resource group '{resource_group}' deleted."}),
            mimetype="application/json",
            status_code=200,
        )
    except Exception as e:
        logging.error("Error deleting resource group: %s", e)
        return func.HttpResponse(
            "Internal error deleting resource group", status_code=500
        )


@app.route(
    route="request_runner", auth_level=func.AuthLevel.ANONYMOUS, methods=["POST"]
)
def request_runner(req: func.HttpRequest) -> func.HttpResponse:
    authorized, reason = require_authenticated_caller(req)
    if not authorized:
        logging.warning("Unauthorized request_runner request: %s", reason)
        return func.HttpResponse("Unauthorized", status_code=401)
    return handle_request_runner(req)


def handle_request_runner(req: func.HttpRequest) -> func.HttpResponse:
    logging.info("Request received")
    try:
        body = req.get_json()
        repo = body["repo"]  # org/repo
        runner_name = body.get("runner-name", "gh-runner-vm")
        resource_group = body.get("resource-group", os.environ["RESOURCE_GROUP"])
        virtualization_flavor = body.get("virtualization-flavor", "hyperv")
        validate_source_group_name(resource_group)
    except Exception:
        return func.HttpResponse(
            'Invalid JSON body. Expected: { "repo": "org/repo" }', status_code=400
        )

    if not _REPO_RE.match(repo):
        return func.HttpResponse(
            "Invalid repo format. Expected: owner/repo", status_code=400
        )
    if virtualization_flavor not in _ALLOWED_FLAVORS:
        return func.HttpResponse(
            f"Invalid virtualization-flavor. Allowed: {', '.join(sorted(_ALLOWED_FLAVORS))}",
            status_code=400,
        )

    # 1. Get PAT from Key Vault
    credential = DefaultAzureCredential()
    vault_url = os.environ["VAULT_URL"]
    kv_client = SecretClient(vault_url=vault_url, credential=credential)

    pat = kv_client.get_secret("github-runner-pat").value

    # 2. Call GitHub API
    url = f"https://api.github.com/repos/{repo}/actions/runners/registration-token"

    response = requests.post(
        url,
        headers={
            "Authorization": f"Bearer {pat}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        timeout=10,
    )

    if response.status_code != 201:
        logging.error("GitHub API error: %s %s", response.status_code, response.text)
        return func.HttpResponse(
            "Failed to obtain runner registration token", status_code=500
        )

    runner_token = response.json()["token"]

    # Load and prepare PowerShell script
    script = load_powershell_script(
        runner_token, f"https://github.com/{repo}", virtualization_flavor
    )

    custom_data = base64.b64encode(script.encode("utf-8")).decode("utf-8")

    # Create VM
    compute = ComputeManagementClient(credential, os.environ["SUBSCRIPTION_ID"])
    resource_client = ResourceManagementClient(
        credential, os.environ["SUBSCRIPTION_ID"]
    )

    # Tag the RG with an ownership marker. This is a defense-in-depth companion to
    # the name-prefix guard: delete_resource_group refuses any RG that lacks this
    # tag, so a bug or a maliciously crafted same-prefix name cannot delete an
    # unrelated resource group.
    resource_client.resource_groups.create_or_update(
        resource_group,
        {
            "location": os.environ["LOCATION"],
            "tags": {RG_OWNER_TAG: RG_OWNER_TAG_VALUE},
        },
    )

    # Support custom image via environment variable (expects ARM resource ID)
    custom_image_id = os.environ.get("CUSTOM_IMAGE_ID")
    if custom_image_id:
        image_reference = {"id": custom_image_id + "-" + virtualization_flavor}
    else:
        # throw error if no custom image provided
        return func.HttpResponse(
            "CUSTOM_IMAGE_ID environment variable not set", status_code=500
        )

    try:
        admin_password = kv_client.get_secret("github-runner-vm-admin-pw").value
    except Exception as e:
        logging.error("Failed to retrieve VM admin password from Key Vault: %s", e)
        return func.HttpResponse(
            "Internal error retrieving credentials", status_code=500
        )

    try:
        # Create network resources
        network_client = NetworkManagementClient(
            credential, os.environ["SUBSCRIPTION_ID"]
        )

        vnet_name = os.environ.get("VNET_NAME", "gh-runner-vnet")
        subnet_name = os.environ.get("SUBNET_NAME", "default")
        ip_name = os.environ.get("IP_NAME", "gh-runner-ip")
        nic_name = os.environ.get("NIC_NAME", "gh-runner-nic")
        location = os.environ["LOCATION"]

        # Create VNet if not exists
        vnet = network_client.virtual_networks.begin_create_or_update(
            resource_group,
            vnet_name,
            {
                "location": location,
                "address_space": {"address_prefixes": ["10.0.0.0/16"]},
            },
        ).result()

        # Create Subnet if not exists
        subnet = network_client.subnets.begin_create_or_update(
            resource_group,
            vnet_name,
            subnet_name,
            {"address_prefix": "10.0.0.0/24"},
        ).result()

        # Create Public IP
        public_ip = network_client.public_ip_addresses.begin_create_or_update(
            resource_group,
            ip_name,
            {
                "location": location,
                "public_ip_allocation_method": "Static",
                "sku": {"name": "Standard"},
            },
        ).result()

        # Create NSG (if not exists).
        #
        # No inbound rules are added: the runner only needs OUTBOUND connectivity
        # to GitHub, and the default NSG rules deny all inbound traffic. RDP (3389)
        # was previously opened to `*` for first-run bootstrap debugging, which is
        # now proven; leaving a public IP with RDP open to the whole internet is an
        # unauthenticated attack surface (brute force, RDP CVEs). For the rare debug
        # session, use Azure Bastion / just-in-time VM access instead of a standing
        # inbound rule.
        nsg_name = os.environ.get("NSG_NAME", "gh-runner-nsg")
        nsg = network_client.network_security_groups.begin_create_or_update(
            resource_group,
            nsg_name,
            {
                "location": location,
            },
        ).result()

        # Create NIC and associate NSG
        nic = network_client.network_interfaces.begin_create_or_update(
            resource_group,
            nic_name,
            {
                "location": location,
                "ip_configurations": [
                    {
                        "name": "ipconfig1",
                        "subnet": {"id": subnet.id},
                        "public_ip_address": {"id": public_ip.id},
                    }
                ],
                "network_security_group": {"id": nsg.id},
            },
        ).result()

        vm_params = {
            "location": location,
            "hardware_profile": {"vm_size": os.environ["VM_SIZE"]},
            "storage_profile": {
                "image_reference": image_reference,
                "os_disk": {
                    "create_option": "FromImage",
                    # disk size needs to be at minium 128GB - defined by the image size
                    # "disk_size_gb": int(os.environ.get("OS_DISK_SIZE_GB", "80")),
                    "managed_disk": {"storage_account_type": "Premium_LRS"},
                },
            },
            "os_profile": {
                # Windows computer names must be ≤15 chars
                "computer_name": runner_name[:15],
                "admin_username": os.environ["ADMIN_USERNAME"],
                "admin_password": admin_password,
                "custom_data": custom_data,
            },
            "network_profile": {"network_interfaces": [{"id": nic.id}]},
        }

        # Configure spot VM if enabled
        use_spot = os.environ.get("VM_USE_SPOT", "false").lower() == "true"
        if use_spot:
            vm_params["priority"] = "Spot"
            vm_params["eviction_policy"] = "Deallocate"
            vm_params["billing_profile"] = {
                "max_price": -1
            }  # -1 means pay up to on-demand price

            try:
                logging.info("Attempting to create spot VM")
                compute.virtual_machines.begin_create_or_update(
                    resource_group, os.environ["VM_NAME"], vm_params
                )
            except Exception as spot_error:
                logging.warning(
                    f"Spot VM creation failed: {str(spot_error)}. Retrying with regular VM."
                )
                # Remove spot configuration and retry
                vm_params.pop("priority", None)
                vm_params.pop("eviction_policy", None)
                vm_params.pop("billing_profile", None)
                compute.virtual_machines.begin_create_or_update(
                    resource_group, os.environ["VM_NAME"], vm_params
                )
        else:
            compute.virtual_machines.begin_create_or_update(
                resource_group, os.environ["VM_NAME"], vm_params
            )
    except Exception as e:
        logging.error("Error creating VM: %s", e)
        return func.HttpResponse("Internal error creating runner VM", status_code=500)

    return func.HttpResponse(
        json.dumps(
            {"message": "Runner VM creation started", "vm": os.environ["VM_NAME"]}
        ),
        mimetype="application/json",
        status_code=202,
    )


def validate_source_group_name(name: str) -> bool:
    if not name.startswith("gh-runner-tmp"):
        raise Exception("Invalid source group name")

    return True


def validate_source_group_ownership(
    resource_client: ResourceManagementClient, name: str
) -> bool:
    """Confirm the resource group carries the broker's ownership tag.

    Refuses deletion if the RG does not exist or was not created by this broker
    (missing/incorrect ``managed-by`` tag), even when its name matches the prefix.
    """
    rg = resource_client.resource_groups.get(name)
    tags = rg.tags or {}
    if tags.get(RG_OWNER_TAG) != RG_OWNER_TAG_VALUE:
        raise Exception(
            f"Resource group '{name}' is not owned by gh-runner-broker; refusing to delete"
        )

    return True
