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
        "azure.mgmt.resource": types.ModuleType("azure.mgmt.resource"),
        "azure.mgmt.network": types.ModuleType("azure.mgmt.network"),
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
    modules["azure.mgmt.resource"].ResourceManagementClient = mock.Mock()
    modules["azure.mgmt.network"].NetworkManagementClient = mock.Mock()
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


if __name__ == "__main__":
    unittest.main()
