"""Unit tests for the gh-runner Function App.

The Azure SDKs are stubbed so the tests run with the Python standard library
only: python3 -m unittest discover -s scripts/gh-runner-func
"""

import json
import os
import sys
import types
import unittest
from unittest import mock


class _HttpResponse:
    def __init__(self, body=None, status_code=200, mimetype=None):
        self.body = body
        self.status_code = status_code
        self.mimetype = mimetype


class _FunctionApp:
    def route(self, *args, **kwargs):
        return lambda handler: handler


class ResourceNotFoundError(Exception):
    pass


class _Model(types.SimpleNamespace):
    """Stand-in for an Azure SDK model: keeps its keyword arguments as attributes."""


def _models_module(name):
    module = types.ModuleType(name)
    classes = {}

    def model_class(class_name):
        if class_name.startswith("__"):
            raise AttributeError(class_name)
        return classes.setdefault(class_name, type(class_name, (_Model,), {}))

    module.__getattr__ = model_class
    return module


def _install_stubs():
    modules = {
        "azure": types.ModuleType("azure"),
        "azure.functions": types.ModuleType("azure.functions"),
        "azure.core": types.ModuleType("azure.core"),
        "azure.core.exceptions": types.ModuleType("azure.core.exceptions"),
        "azure.identity": types.ModuleType("azure.identity"),
        "azure.keyvault": types.ModuleType("azure.keyvault"),
        "azure.keyvault.secrets": types.ModuleType("azure.keyvault.secrets"),
        "azure.mgmt": types.ModuleType("azure.mgmt"),
        "azure.mgmt.compute": types.ModuleType("azure.mgmt.compute"),
        "azure.mgmt.compute.models": _models_module("azure.mgmt.compute.models"),
        "azure.mgmt.resource": types.ModuleType("azure.mgmt.resource"),
        "azure.mgmt.resource.resources": types.ModuleType("azure.mgmt.resource.resources"),
        "azure.mgmt.resource.resources.models": _models_module("azure.mgmt.resource.resources.models"),
        "azure.mgmt.network": types.ModuleType("azure.mgmt.network"),
        "azure.mgmt.network.models": _models_module("azure.mgmt.network.models"),
        "requests": types.ModuleType("requests"),
    }
    functions = modules["azure.functions"]
    functions.FunctionApp = _FunctionApp
    functions.HttpResponse = _HttpResponse
    functions.HttpRequest = object
    functions.AuthLevel = types.SimpleNamespace(ANONYMOUS="anonymous")
    modules["azure.core.exceptions"].ResourceNotFoundError = ResourceNotFoundError
    modules["azure.identity"].DefaultAzureCredential = mock.Mock()
    modules["azure.keyvault.secrets"].SecretClient = mock.Mock()
    modules["azure.mgmt.compute"].ComputeManagementClient = mock.Mock()
    modules["azure.mgmt.resource.resources"].ResourceManagementClient = mock.Mock()
    modules["azure.mgmt.network"].NetworkManagementClient = mock.Mock()
    modules["azure.mgmt.compute"].models = modules["azure.mgmt.compute.models"]
    modules["azure.mgmt.network"].models = modules["azure.mgmt.network.models"]
    for name, module in modules.items():
        sys.modules.setdefault(name, module)


_install_stubs()
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import function_app  # noqa: E402


def _request(body):
    return types.SimpleNamespace(get_json=lambda: body)


def _resource_group(tags=None, provisioning_state="Succeeded"):
    return types.SimpleNamespace(
        tags=tags,
        properties=types.SimpleNamespace(provisioning_state=provisioning_state),
    )


OWNED_TAGS = {function_app.RG_OWNER_TAG: function_app.RG_OWNER_TAG_VALUE}


class DeleteResourceGroupTest(unittest.TestCase):
    def setUp(self):
        self.client = mock.Mock()
        patches = [
            mock.patch.object(function_app, "ResourceManagementClient", return_value=self.client),
            mock.patch.object(function_app, "DefaultAzureCredential"),
            mock.patch.dict(os.environ, {"SUBSCRIPTION_ID": "sub", "RESOURCE_GROUP": "unused"}),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)

    def _delete(self, resource_group="gh-runner-tmp-1-hv"):
        response = function_app.handle_delete_resource_group(_request({"resource-group": resource_group}))
        body = json.loads(response.body) if response.status_code < 300 else response.body
        return response.status_code, body

    def test_starts_deletion_without_waiting(self):
        self.client.resource_groups.get.return_value = _resource_group(OWNED_TAGS)

        status_code, body = self._delete()

        self.assertEqual(status_code, 202)
        self.assertEqual(body["status"], function_app.DELETE_STATUS_DELETING)
        self.client.resource_groups.begin_delete.assert_called_once_with("gh-runner-tmp-1-hv")
        poller = self.client.resource_groups.begin_delete.return_value
        poller.wait.assert_not_called()
        poller.result.assert_not_called()

    def test_reports_deleted_when_resource_group_is_gone(self):
        self.client.resource_groups.get.side_effect = ResourceNotFoundError("not found")

        status_code, body = self._delete()

        self.assertEqual(status_code, 200)
        self.assertEqual(body["status"], function_app.DELETE_STATUS_DELETED)
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_reports_deleting_without_new_request_while_deletion_runs(self):
        self.client.resource_groups.get.return_value = _resource_group(OWNED_TAGS, "Deleting")

        status_code, body = self._delete()

        self.assertEqual(status_code, 202)
        self.assertEqual(body["status"], function_app.DELETE_STATUS_DELETING)
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_refuses_resource_group_without_ownership_tag(self):
        for tags in (None, {}, {function_app.RG_OWNER_TAG: "someone-else"}):
            self.client.resource_groups.get.return_value = _resource_group(tags)

            status_code, _ = self._delete()

            self.assertEqual(status_code, 500, tags)
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_refuses_name_outside_runner_prefix(self):
        status_code, _ = self._delete("production-rg")

        self.assertEqual(status_code, 500)
        self.client.resource_groups.get.assert_not_called()
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_ownership_is_checked_before_reporting_deleting(self):
        self.client.resource_groups.get.return_value = _resource_group({}, "Deleting")

        status_code, _ = self._delete()

        self.assertEqual(status_code, 500)


REQUEST_RUNNER_ENV = {
    "VAULT_URL": "https://vault",
    "SUBSCRIPTION_ID": "sub",
    "LOCATION": "westeurope",
    "VM_NAME": "gh-runner-vm",
    "VM_SIZE": "Standard_D4s_v5",
    "ADMIN_USERNAME": "runneradmin",
    "CUSTOM_IMAGE_ID": "/images/gh-runner",
    "RESOURCE_GROUP": "unused",
}


class RequestRunnerTest(unittest.TestCase):
    def setUp(self):
        self.network = mock.Mock()
        self.compute = mock.Mock()
        self.resources = mock.Mock()
        secrets = mock.Mock()
        secrets.get_secret.return_value.value = "secret"
        github = mock.Mock(status_code=201)
        github.json.return_value = {"token": "registration-token"}
        patches = [
            mock.patch.object(function_app, "NetworkManagementClient", return_value=self.network),
            mock.patch.object(function_app, "ComputeManagementClient", return_value=self.compute),
            mock.patch.object(function_app, "ResourceManagementClient", return_value=self.resources),
            mock.patch.object(function_app, "SecretClient", return_value=secrets),
            mock.patch.object(function_app, "DefaultAzureCredential"),
            mock.patch.object(function_app.requests, "post", create=True, return_value=github),
            mock.patch.dict(os.environ, REQUEST_RUNNER_ENV),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)

    def _request_runner(self):
        return function_app.handle_request_runner(
            _request({"repo": "owner/repo", "resource-group": "gh-runner-tmp-1-hv", "runner-name": "1-hv"})
        )

    def _bodies(self):
        operations = {
            "ResourceGroup": self.resources.resource_groups.create_or_update,
            "VirtualNetwork": self.network.virtual_networks.begin_create_or_update,
            "Subnet": self.network.subnets.begin_create_or_update,
            "PublicIPAddress": self.network.public_ip_addresses.begin_create_or_update,
            "NetworkSecurityGroup": self.network.network_security_groups.begin_create_or_update,
            "NetworkInterface": self.network.network_interfaces.begin_create_or_update,
        }
        bodies = {model: operation.call_args.args[-1] for model, operation in operations.items()}
        for call in self.compute.virtual_machines.begin_create_or_update.call_args_list:
            bodies.setdefault("VirtualMachine", []).append(call.args[-1])
        return bodies

    def _assert_no_dict_bodies(self, value, path):
        # The SDKs send dict bodies verbatim, so a nested dict means a request ARM
        # rejects. Tags are a genuine string map and stay a dict.
        if isinstance(value, dict):
            self.fail(f"plain dict in request body at {path}")
        if isinstance(value, list):
            for index, item in enumerate(value):
                self._assert_no_dict_bodies(item, f"{path}[{index}]")
        if isinstance(value, _Model):
            for name, item in vars(value).items():
                if name != "tags":
                    self._assert_no_dict_bodies(item, f"{path}.{name}")

    def test_builds_request_bodies_from_sdk_models(self):
        response = self._request_runner()

        self.assertEqual(response.status_code, 202)
        bodies = self._bodies()
        for model, body in bodies.items():
            for item in body if isinstance(body, list) else [body]:
                self.assertEqual(type(item).__name__, model)
                self._assert_no_dict_bodies(item, model)
        self.assertEqual(bodies["ResourceGroup"].tags, OWNED_TAGS)

    def test_spot_fallback_drops_spot_settings(self):
        self.compute.virtual_machines.begin_create_or_update.side_effect = [Exception("no capacity"), mock.DEFAULT]

        with mock.patch.dict(os.environ, {"VM_USE_SPOT": "true"}), self.assertLogs(level="WARNING"):
            response = self._request_runner()

        self.assertEqual(response.status_code, 202)
        spot_vm, regular_vm = self._bodies()["VirtualMachine"]
        self.assertEqual(spot_vm.priority, "Spot")
        for field in ("priority", "eviction_policy", "billing_profile"):
            self.assertFalse(hasattr(regular_vm, field), field)


if __name__ == "__main__":
    unittest.main()
