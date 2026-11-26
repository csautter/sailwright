"""Unit tests for the gh-runner Function App.

The Azure SDKs are stubbed so the tests run with the Python standard library
only: python3 -m unittest discover -s scripts/gh-runner-func
"""

import json
import os
import sys
import types
import unittest
from concurrent.futures import ThreadPoolExecutor
from threading import Event, Lock
from unittest import mock


class _HttpResponse:
    def __init__(self, body=None, status_code=200, mimetype=None, headers=None):
        self.body = body
        self.status_code = status_code
        self.mimetype = mimetype
        self.headers = headers or {}


class _FunctionApp:
    def route(self, *args, **kwargs):
        return lambda handler: handler


class AzureError(Exception):
    pass


class HttpResponseError(AzureError):
    def __init__(self, message="upstream error", status_code=500, code=None):
        super().__init__(message)
        self.status_code = status_code
        self.error = types.SimpleNamespace(code=code)


class ResourceNotFoundError(HttpResponseError):
    pass


class ServiceRequestError(AzureError):
    pass


class ServiceResponseError(AzureError):
    pass


class RetryPolicy:
    def __init__(self, **kwargs):
        self.kwargs = kwargs

    def get_retry_after(self, response):
        return response


class RequestException(Exception):
    pass


class Timeout(RequestException):
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
        "azure.core.pipeline": types.ModuleType("azure.core.pipeline"),
        "azure.core.pipeline.policies": types.ModuleType(
            "azure.core.pipeline.policies"
        ),
        "azure.identity": types.ModuleType("azure.identity"),
        "azure.keyvault": types.ModuleType("azure.keyvault"),
        "azure.keyvault.secrets": types.ModuleType("azure.keyvault.secrets"),
        "azure.mgmt": types.ModuleType("azure.mgmt"),
        "azure.mgmt.compute": types.ModuleType("azure.mgmt.compute"),
        "azure.mgmt.compute.models": _models_module("azure.mgmt.compute.models"),
        "azure.mgmt.resource": types.ModuleType("azure.mgmt.resource"),
        "azure.mgmt.resource.resources": types.ModuleType(
            "azure.mgmt.resource.resources"
        ),
        "azure.mgmt.resource.resources.models": _models_module(
            "azure.mgmt.resource.resources.models"
        ),
        "azure.mgmt.network": types.ModuleType("azure.mgmt.network"),
        "azure.mgmt.network.models": _models_module("azure.mgmt.network.models"),
        "requests": types.ModuleType("requests"),
    }
    functions = modules["azure.functions"]
    functions.FunctionApp = _FunctionApp
    functions.HttpResponse = _HttpResponse
    functions.HttpRequest = object
    functions.AuthLevel = types.SimpleNamespace(ANONYMOUS="anonymous")
    modules["azure.core"].MatchConditions = types.SimpleNamespace(IfMissing="IfMissing")
    for cls in (
        AzureError,
        HttpResponseError,
        ResourceNotFoundError,
        ServiceRequestError,
        ServiceResponseError,
    ):
        setattr(modules["azure.core.exceptions"], cls.__name__, cls)
    modules["azure.core.pipeline.policies"].RetryPolicy = RetryPolicy
    modules["requests"].RequestException = RequestException
    modules["requests"].Timeout = Timeout
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


def _request(body, headers=None):
    return types.SimpleNamespace(get_json=lambda: body, headers=dict(headers or {}))


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
            mock.patch.object(
                function_app, "ResourceManagementClient", return_value=self.client
            ),
            mock.patch.object(function_app, "DefaultAzureCredential"),
            mock.patch.dict(
                os.environ, {"SUBSCRIPTION_ID": "sub", "RESOURCE_GROUP": "unused"}
            ),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)

    def _delete(self, resource_group="gh-runner-tmp-1-hv"):
        response = function_app.handle_delete_resource_group(
            _request({"resource-group": resource_group})
        )
        return response.status_code, json.loads(response.body)

    def test_starts_deletion_without_waiting(self):
        self.client.resource_groups.get.return_value = _resource_group(OWNED_TAGS)

        status_code, body = self._delete()

        self.assertEqual(status_code, 202)
        self.assertEqual(body["status"], function_app.DELETE_STATUS_DELETING)
        self.client.resource_groups.begin_delete.assert_called_once_with(
            "gh-runner-tmp-1-hv", polling=False
        )
        self.assertEqual(body["resource-group"], "gh-runner-tmp-1-hv")
        self.assertIn("correlation-id", body)
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
        self.client.resource_groups.get.return_value = _resource_group(
            OWNED_TAGS, "Deleting"
        )

        status_code, body = self._delete()

        self.assertEqual(status_code, 202)
        self.assertEqual(body["status"], function_app.DELETE_STATUS_DELETING)
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_refuses_resource_group_without_ownership_tag(self):
        for tags in (None, {}, {function_app.RG_OWNER_TAG: "someone-else"}):
            self.client.resource_groups.get.return_value = _resource_group(tags)

            status_code, _ = self._delete()

            self.assertEqual(status_code, 403, tags)
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_refuses_name_outside_runner_prefix(self):
        for name in ("production-rg", "gh-runner-tmp/../x", "gh-runner-tmp-x."):
            status_code, _ = self._delete(name)

            self.assertEqual(status_code, 400, name)
        self.client.resource_groups.get.assert_not_called()
        self.client.resource_groups.begin_delete.assert_not_called()

    def test_ownership_is_checked_before_reporting_deleting(self):
        self.client.resource_groups.get.return_value = _resource_group({}, "Deleting")

        status_code, _ = self._delete()

        self.assertEqual(status_code, 403)

    def test_dependency_failure_is_structured(self):
        self.client.resource_groups.get.side_effect = HttpResponseError(
            "private-secret", 503, "ServerBusy"
        )
        with self.assertLogs(level="ERROR") as logs:
            status_code, body = self._delete()
        self.assertEqual(status_code, 503)
        self.assertEqual(body["stage"], "resource-group")
        self.assertIn('"upstream-status": 503', str(logs.output))
        self.assertNotIn("private-secret", json.dumps(body) + str(logs.output))

    def test_missing_subscription_is_reported(self):
        with mock.patch.dict(os.environ, {"SUBSCRIPTION_ID": ""}):
            status_code, body = self._delete()
        self.assertEqual(status_code, 503)
        self.assertEqual(body["missing"], ["SUBSCRIPTION_ID"])
        self.client.resource_groups.get.assert_not_called()


REQUEST_RUNNER_ENV = {
    "ALLOWED_TENANT_ID": "tenant",
    "ALLOWED_AUDIENCE": "api://broker",
    "VAULT_URL": "https://vault",
    "SUBSCRIPTION_ID": "sub",
    "LOCATION": "westeurope",
    "VM_NAME": "gh-runner-vm",
    "VM_SIZE": "Standard_D4s_v5",
    "ADMIN_USERNAME": "runneradmin",
    "CUSTOM_IMAGE_ID": "/images/gh-runner",
    "ALLOWED_REPOS": "owner/repo",
    "IDEMPOTENCY_STORAGE_URL": "https://test.blob.core.windows.net/runner-requests",
    "RESOURCE_GROUP": "unused",
}


class _BlobStorage:
    """Simulate Azure's conditional creation and lease checks at the HTTP boundary."""

    def __init__(self):
        self.lock = Lock()
        self.records = {}
        self.leases = {}
        self.reject_renewal = False

    def request(self, method, url, headers, params=None, json=None, **options):
        def response(status, body=None, code=""):
            return mock.Mock(
                status_code=status, headers={"x-ms-error-code": code},
                **{"json.return_value": body},
            )

        if not url.startswith(REQUEST_RUNNER_ENV["IDEMPOTENCY_STORAGE_URL"] + "/"):
            raise AssertionError("Unexpected storage URL")
        with self.lock:
            lease = self.leases.get(url)
            if method == "GET":
                return response(200, self.records[url]) if url in self.records else response(404)
            if method != "PUT":
                raise AssertionError(f"Unexpected method {method}")
            if params == {"comp": "lease"}:
                if url not in self.records:
                    return response(404)
                action = headers["x-ms-lease-action"]
                if action == "acquire":
                    if lease is not None:
                        return response(409, code="LeaseAlreadyPresent")
                    assert headers["x-ms-lease-duration"] == "60"
                    self.leases[url] = headers["x-ms-proposed-lease-id"]
                    return response(201)
                if lease is None or headers["x-ms-lease-id"] != lease:
                    return response(409)
                if action == "release":
                    del self.leases[url]
                elif action == "renew":
                    if self.reject_renewal:
                        return response(409)
                else:
                    raise AssertionError(f"Unexpected lease action {action}")
                return response(200)
            if headers.get("If-None-Match") == "*":
                if url in self.records:
                    return response(412)
            elif lease is None or headers["x-ms-lease-id"] != lease:
                return response(412)
            assert headers["x-ms-blob-type"] == "BlockBlob"
            self.records[url] = json
            return response(201)


class RequestRunnerTest(unittest.TestCase):
    def setUp(self):
        self.network = mock.Mock()
        self.compute = mock.Mock()
        self.resources = mock.Mock()
        self.storage = _BlobStorage()
        for operations in (
            self.network.virtual_networks,
            self.network.subnets,
            self.network.public_ip_addresses,
            self.network.network_security_groups,
            self.network.network_interfaces,
        ):
            operations.get.side_effect = ResourceNotFoundError()
        # A new group by default; tests that reuse one set a return value.
        self.resources.resource_groups.get.side_effect = ResourceNotFoundError()
        secrets = self.secrets = mock.Mock()
        secrets.get_secret.return_value.value = "secret"
        github = self.github = mock.Mock(status_code=201)
        github.json.return_value = {"token": "registration-token"}
        function_app._runner_config.cache_clear()
        self.addCleanup(function_app._runner_config.cache_clear)
        patches = [
            mock.patch.object(
                function_app, "NetworkManagementClient", return_value=self.network
            ),
            mock.patch.object(
                function_app, "ComputeManagementClient", return_value=self.compute
            ),
            mock.patch.object(
                function_app, "ResourceManagementClient", return_value=self.resources
            ),
            mock.patch.object(function_app, "SecretClient", return_value=secrets),
            mock.patch.object(function_app, "DefaultAzureCredential"),
            mock.patch.object(
                function_app.requests, "post", create=True, return_value=github
            ),
            mock.patch.object(
                function_app.requests, "request", create=True, side_effect=self.storage.request
            ),
            mock.patch.dict(os.environ, REQUEST_RUNNER_ENV),
            mock.patch.object(function_app, "sleep"),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)

    def _request_runner(self, headers=None, **overrides):
        body = {
            "repo": "owner/repo",
            "resource-group": "gh-runner-tmp-1-hv",
            "runner-name": "1-hv",
            **overrides,
        }
        return function_app.handle_request_runner(_request(body, headers))

    def _bodies(self):
        operations = {
            "ResourceGroup": self.resources.resource_groups.create_or_update,
            "VirtualNetwork": self.network.virtual_networks.begin_create_or_update,
            "Subnet": self.network.subnets.begin_create_or_update,
            "PublicIPAddress": self.network.public_ip_addresses.begin_create_or_update,
            "NetworkSecurityGroup": self.network.network_security_groups.begin_create_or_update,
            "NetworkInterface": self.network.network_interfaces.begin_create_or_update,
        }
        bodies = {
            model: operation.call_args.args[-1]
            for model, operation in operations.items()
        }
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
        self.compute.virtual_machines.begin_create_or_update.side_effect = [
            function_app.HttpResponseError("no capacity"),
            mock.DEFAULT,
        ]

        with (
            mock.patch.dict(os.environ, {"VM_USE_SPOT": "true"}),
            self.assertLogs(level="WARNING"),
        ):
            response = self._request_runner()

        self.assertEqual(response.status_code, 202)
        spot_vm, regular_vm = self._bodies()["VirtualMachine"]
        self.assertEqual(spot_vm.priority, "Spot")
        self.assertEqual(spot_vm.eviction_policy, "Deallocate")
        self.assertEqual(spot_vm.billing_profile.max_price, -1)
        for field in ("priority", "eviction_policy", "billing_profile"):
            self.assertFalse(hasattr(regular_vm, field), field)

    def test_regular_vm_has_no_spot_settings(self):
        response = self._request_runner()

        self.assertEqual(response.status_code, 202)
        (vm,) = self._bodies()["VirtualMachine"]
        for field in ("priority", "eviction_policy", "billing_profile"):
            self.assertFalse(hasattr(vm, field), field)

    def test_spot_model_error_does_not_fall_back_to_regular_vm(self):
        virtual_machine = function_app.compute_models.VirtualMachine

        def build(**kwargs):
            if "priority" in kwargs:
                raise TypeError("unexpected keyword argument")
            return virtual_machine(**kwargs)

        with (
            mock.patch.dict(os.environ, {"VM_USE_SPOT": "true"}),
            mock.patch.object(
                function_app.compute_models, "VirtualMachine", side_effect=build
            ),
            self.assertLogs(level="ERROR"),
        ):
            response = self._request_runner()

        self.assertEqual(response.status_code, 500)
        self.assertEqual(json.loads(response.body)["stage"], "vm")
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()

    def test_missing_configuration_is_cached_and_deterministic(self):
        with mock.patch.dict(os.environ, {"VAULT_URL": "", "VM_SIZE": ""}):
            response = self._request_runner()
        self.assertEqual(response.status_code, 503)
        self.assertEqual(json.loads(response.body)["missing"], ["VAULT_URL", "VM_SIZE"])
        self.assertEqual(self._request_runner().status_code, 503)
        self.secrets.get_secret.assert_not_called()

    def test_key_vault_transport_failure_retries_without_leaking_secrets(self):
        self.secrets.get_secret.side_effect = ServiceRequestError("PAT=private-secret")
        with self.assertLogs(level="WARNING") as logs:
            response = self._request_runner()
        self.assertEqual(response.status_code, 503)
        self.assertEqual(self.secrets.get_secret.call_count, 3)
        self.assertEqual(
            function_app.sleep.call_args_list, [mock.call(0.5), mock.call(1.0)]
        )
        self.assertNotIn("private-secret", response.body + str(logs.output))
        self.assertEqual(json.loads(response.body)["stage"], "pat")

    def test_key_vault_denial_is_not_retried(self):
        self.secrets.get_secret.side_effect = HttpResponseError(status_code=403)
        self.assertEqual(self._request_runner().status_code, 502)
        self.secrets.get_secret.assert_called_once()

    def test_admin_password_failure_is_mapped_before_resources_are_created(self):
        self.secrets.get_secret.side_effect = [
            types.SimpleNamespace(value="pat"),
            ServiceResponseError("private-secret"),
            ServiceResponseError(),
            ServiceResponseError(),
        ]
        response = self._request_runner()
        self.assertEqual(response.status_code, 503)
        self.assertEqual(json.loads(response.body)["stage"], "admin-password")
        self.resources.resource_groups.create_or_update.assert_not_called()

    def test_key_vault_recovers_after_transient_failure(self):
        self.secrets.get_secret.side_effect = [
            ServiceRequestError(),
            types.SimpleNamespace(value="pat"),
            types.SimpleNamespace(value="password"),
        ]
        self.assertEqual(self._request_runner().status_code, 202)
        self.assertEqual(self.secrets.get_secret.call_count, 3)

    def test_github_timeout_and_network_failure(self):
        for error, expected in [
            (Timeout("private-secret"), 504),
            (RequestException("private-secret"), 502),
        ]:
            with (
                self.subTest(error=type(error).__name__),
                mock.patch.object(
                    function_app.requests, "post", side_effect=error
                ) as post,
            ):
                with self.assertLogs(level="WARNING") as logs:
                    response = self._request_runner()
                self.assertEqual(response.status_code, expected)
                self.assertEqual(post.call_count, 3)
                self.assertNotIn("private-secret", response.body + str(logs.output))
                self.assertEqual(json.loads(response.body)["stage"], "github-token")

    def test_github_status_mapping(self):
        for status, expected, attempts in [
            (401, 502, 1),
            (403, 502, 1),
            (404, 400, 1),
            (429, 503, 1),
            (500, 502, 3),
            (503, 502, 3),
            (302, 502, 1),
            (422, 502, 1),
        ]:
            with self.subTest(status=status):
                self.github.status_code = status
                self.github.headers = {"Retry-After": "90"}
                function_app.requests.post.reset_mock()
                response = self._request_runner()
                self.assertEqual(response.status_code, expected)
                self.assertEqual(function_app.requests.post.call_count, attempts)
                if status == 429:
                    self.assertEqual(response.headers["Retry-After"], "90")
                self.compute.virtual_machines.begin_create_or_update.assert_not_called()
                self.resources.resource_groups.create_or_update.assert_not_called()
                self.network.virtual_networks.begin_create_or_update.assert_not_called()

    def test_github_invalid_response(self):
        for body in (None, {}, {"token": None}, {"token": ""}, []):
            with self.subTest(body=body):
                self.github.json.return_value = body
                self.assertEqual(self._request_runner().status_code, 502)
        self.github.json.side_effect = ValueError("private-secret")
        self.assertEqual(self._request_runner().status_code, 502)

    def test_github_retry_recovers(self):
        with mock.patch.object(
            function_app.requests, "post", side_effect=[Timeout(), self.github]
        ) as post:
            self.assertEqual(self._request_runner().status_code, 202)
        self.assertEqual(post.call_count, 2)
        self.assertEqual(post.call_args.kwargs["timeout"], (3, 5))
        self.assertFalse(post.call_args.kwargs["allow_redirects"])

    def test_invalid_inputs_do_not_call_dependencies(self):
        for body in (
            None,
            [],
            {},
            {"repo": 12},
            {"repo": "../repo"},
            {"repo": "owner/."},
            {"repo": "owner/.."},
            {"repo": "owner/repo", "virtualization-flavor": []},
            {"repo": "owner/repo", "resource-group": "production"},
        ):
            with self.subTest(body=body):
                response = function_app.handle_request_runner(_request(body))
                self.assertEqual(response.status_code, 400)
        self.secrets.get_secret.assert_not_called()

    def test_resource_group_failure_has_stage_and_arm_code(self):
        self.resources.resource_groups.create_or_update.side_effect = HttpResponseError(
            "private-secret", 429, "TooManyRequests"
        )
        with self.assertLogs(level="ERROR") as logs:
            response = self._request_runner()
        self.assertEqual(response.status_code, 503)
        self.assertEqual(json.loads(response.body)["stage"], "resource-group")
        self.assertIn("TooManyRequests", str(logs.output))
        self.assertNotIn("private-secret", str(logs.output) + response.body)

    def test_authentication_and_correlation(self):
        request = _request({})
        request.headers["X-Correlation-ID"] = "ci-run-123"
        with (
            mock.patch.object(
                function_app,
                "require_authenticated_caller",
                return_value=(False, "secret"),
            ),
            self.assertLogs(level="INFO") as logs,
        ):
            response = function_app.request_runner(request)
        self.assertEqual(response.status_code, 401)
        self.assertEqual(response.headers["X-Correlation-ID"], "ci-run-123")
        self.assertIn('"stage": "auth"', str(logs.output))
        self.assertNotIn("secret", str(logs.output))

    def test_invalid_correlation_is_replaced(self):
        request = _request({})
        request.headers["X-Correlation-ID"] = "bad\nheader"
        response = function_app.handle_request_runner(request)
        self.assertRegex(response.headers["X-Correlation-ID"], r"^[a-f0-9]{32}$")

    def test_network_poller_timeout_stops_before_vm_creation(self):
        poller = self.network.virtual_networks.begin_create_or_update.return_value
        poller.done.return_value = False
        response = self._request_runner()
        self.assertEqual(response.status_code, 504)
        self.assertEqual(json.loads(response.body)["stage"], "network")
        poller.result.assert_called_once_with(timeout=20)
        self.network.subnets.begin_create_or_update.assert_not_called()
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()

    def test_network_failure_is_mapped(self):
        self.network.network_interfaces.begin_create_or_update.return_value.result.side_effect = HttpResponseError(
            status_code=409
        )
        self.assertEqual(self._request_runner().status_code, 409)
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()

    def test_concurrent_requests_use_distinct_resource_names_in_the_same_group(self):
        with ThreadPoolExecutor(max_workers=2) as pool:
            responses = list(pool.map(lambda _: self._request_runner(), range(2)))
        bodies = [json.loads(response.body) for response in responses]
        for response in responses:
            self.assertEqual(response.status_code, 202)
        for resource in ("vm", "vnet", "ip", "nic", "nsg"):
            self.assertNotEqual(
                bodies[0]["resources"][resource], bodies[1]["resources"][resource]
            )
        for body in bodies:
            self.assertEqual(body["vm"], body["resources"]["vm"])
            self.assertEqual(body["resource-group"], "gh-runner-tmp-1-hv")
        calls = self.compute.virtual_machines.begin_create_or_update.call_args_list
        self.assertEqual(
            {call.args[1] for call in calls}, {body["vm"] for body in bodies}
        )

    def test_resource_name_prefixes_follow_azure_limits(self):
        config = {"VM_NAME": "Invalid_Name!" * 20}
        with mock.patch.dict(
            os.environ, {"VNET_NAME": "x" * 100, "NIC_NAME": "!!", "NSG_NAME": "ü/"}
        ):
            names = function_app._resource_names(config)
        for resource, name in names.items():
            self.assertRegex(name, r"^[a-z0-9][a-z0-9-]*[a-z0-9]$")
            self.assertLessEqual(len(name), 64 if resource in ("vm", "vnet") else 80)
        self.assertRegex(names["nic"], r"^runner-")

    def test_conflict_returns_generated_names_for_cleanup(self):
        self.compute.virtual_machines.begin_create_or_update.side_effect = (
            HttpResponseError(status_code=409)
        )
        response = self._request_runner()
        body = json.loads(response.body)
        self.assertEqual(response.status_code, 409)
        self.assertRegex(body["vm"], r"^gh-runner-vm-[a-f0-9]{32}$")
        self.assertEqual(body["resource-group"], "gh-runner-tmp-1-hv")

    def test_vm_submission_does_not_start_a_background_poller(self):
        self.assertEqual(self._request_runner().status_code, 202)
        operation = self.compute.virtual_machines.begin_create_or_update
        self.assertIs(operation.call_args.kwargs["polling"], False)
        operation.return_value.result.assert_not_called()
        operation.return_value.wait.assert_not_called()

    def test_spot_failure_logs_arm_code_and_failed_fallback_is_mapped(self):
        self.compute.virtual_machines.begin_create_or_update.side_effect = [
            HttpResponseError(
                "private-secret", 400, "OverconstrainedAllocationRequest"
            ),
            HttpResponseError("private-secret", 503, "AllocationFailed"),
        ]
        with (
            mock.patch.dict(os.environ, {"VM_USE_SPOT": "true"}),
            self.assertLogs(level="WARNING") as logs,
        ):
            response = self._request_runner()
        self.assertEqual(response.status_code, 503)
        self.assertIn("OverconstrainedAllocationRequest", str(logs.output))
        self.assertIn("AllocationFailed", str(logs.output))
        self.assertNotIn("private-secret", response.body + str(logs.output))
        for call in self.compute.virtual_machines.begin_create_or_update.call_args_list:
            self.assertIs(call.kwargs["polling"], False)

    def test_missing_auth_config_logs_setting_names_without_returning_them(self):
        request = _request({})
        with (
            mock.patch.object(
                function_app,
                "require_authenticated_caller",
                return_value=(False, "server misconfiguration"),
            ),
            mock.patch.dict(os.environ, {"ALLOWED_AUDIENCE": ""}),
            self.assertLogs(level="ERROR") as logs,
        ):
            response = function_app.request_runner(request)
        self.assertEqual(response.status_code, 503)
        self.assertNotIn("missing", json.loads(response.body))
        self.assertNotIn("ALLOWED_AUDIENCE", response.body)
        self.assertIn("ALLOWED_AUDIENCE", str(logs.output))

    def test_valid_correlation_propagates_through_all_stages(self):
        request = _request({"repo": "owner/repo", "resource-group": "gh-runner-tmp-ci"})
        request.headers["X-Correlation-ID"] = "ci-run-123"
        with (
            mock.patch.object(
                function_app, "require_authenticated_caller", return_value=(True, "")
            ),
            self.assertLogs(level="INFO") as logs,
        ):
            response = function_app.request_runner(request)
        self.assertEqual(response.status_code, 202)
        self.assertEqual(response.headers["X-Correlation-ID"], "ci-run-123")
        events = [json.loads(record.getMessage()) for record in logs.records]
        self.assertTrue(
            all(event["correlation-id"] == "ci-run-123" for event in events)
        )
        self.assertTrue(
            {"auth", "input", "pat", "github-token", "network", "vm"}.issubset(
                {event["stage"] for event in events}
            )
        )

    def test_network_wait_respects_remaining_request_budget(self):
        poller = self.network.virtual_networks.begin_create_or_update.return_value
        poller.done.return_value = False
        # Request start, pre-write budget check, first network wait.
        with mock.patch.object(function_app, "monotonic", side_effect=[0, 50, 140]):
            response = self._request_runner()
        self.assertEqual(response.status_code, 504)
        poller.result.assert_called_once_with(timeout=0)
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()

    def test_exhausted_budget_stops_before_the_first_write(self):
        with mock.patch.object(function_app, "monotonic", side_effect=[0, 110]):
            response = self._request_runner()
        body = json.loads(response.body)
        self.assertEqual(response.status_code, 504)
        self.assertEqual(body["stage"], "resource-group")
        self.assertEqual(response.headers["Retry-After"], "5")
        self.assertNotIn("cleanup-required", body)
        self.resources.resource_groups.get.assert_not_called()
        self.resources.resource_groups.create_or_update.assert_not_called()

    def test_failures_after_the_first_write_require_cleanup_without_retry_after(self):
        for operation, error in (
            (
                self.compute.virtual_machines.begin_create_or_update,
                ServiceResponseError("private-secret"),
            ),
            (
                self.resources.resource_groups.create_or_update,
                HttpResponseError("private-secret", 503),
            ),
        ):
            with self.subTest(operation=operation):
                operation.side_effect = error
                response = self._request_runner()
                body = json.loads(response.body)
                self.assertEqual(response.status_code, 503)
                self.assertNotIn("Retry-After", response.headers)
                self.assertTrue(body["cleanup-required"])
                self.assertEqual(
                    set(body["resources"]), {"vm", "vnet", "ip", "nic", "nsg"}
                )
                self.assertEqual(body["vm"], body["resources"]["vm"])
                self.assertNotIn("private-secret", response.body)
                operation.side_effect = None

    def test_failures_before_the_first_write_keep_retry_after(self):
        self.secrets.get_secret.side_effect = ServiceRequestError()
        response = self._request_runner()
        self.assertEqual(response.headers["Retry-After"], "5")
        self.assertNotIn("cleanup-required", json.loads(response.body))

    def test_reuses_an_owned_resource_group_without_rewriting_it(self):
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        self.assertEqual(self._request_runner().status_code, 202)
        self.resources.resource_groups.create_or_update.assert_not_called()
        self.compute.virtual_machines.begin_create_or_update.assert_called_once()

    def test_refuses_foreign_or_deleting_resource_groups(self):
        self.resources.resource_groups.get.side_effect = None
        for group in (
            _resource_group(None),
            _resource_group({function_app.RG_OWNER_TAG: "someone-else"}),
            _resource_group(OWNED_TAGS, "Deleting"),
        ):
            with self.subTest(group=group):
                self.resources.resource_groups.get.return_value = group
                response = self._request_runner()
                self.assertEqual(response.status_code, 409)
                self.assertNotIn("cleanup-required", json.loads(response.body))
        self.resources.resource_groups.create_or_update.assert_not_called()
        self.network.virtual_networks.begin_create_or_update.assert_not_called()

    def test_rejects_repositories_outside_the_allowlist(self):
        with self.assertLogs(level="WARNING") as logs:
            response = self._request_runner({"Idempotency-Key": "k"}, repo="owner/other")
        self.assertEqual(response.status_code, 403)
        self.assertIn("repo-rejected", str(logs.output))
        self.secrets.get_secret.assert_not_called()
        self.resources.resource_groups.get.assert_not_called()
        function_app.DefaultAzureCredential.assert_not_called()
        function_app.requests.request.assert_not_called()

    def test_allowlist_is_case_insensitive_and_accepts_several_repositories(self):
        with mock.patch.dict(
            os.environ, {"ALLOWED_REPOS": " owner/.github , Owner/Repo "}
        ):
            function_app._runner_config.cache_clear()
            self.assertEqual(self._request_runner().status_code, 202)
            self.assertEqual(self._request_runner(repo="OWNER/.GitHub").status_code, 202)
            self.assertEqual(self._request_runner(repo="owner/third").status_code, 403)

    def test_empty_or_invalid_repository_allowlist_blocks_all_requests(self):
        for setting in ("", ", ,", "owner/repo, owner/.."):
            with self.subTest(setting=setting), mock.patch.dict(
                os.environ, {"ALLOWED_REPOS": setting}
            ):
                function_app._runner_config.cache_clear()
                response = self._request_runner()
                self.assertEqual(response.status_code, 503)
                self.assertEqual(json.loads(response.body)["missing"], ["ALLOWED_REPOS"])
        self.secrets.get_secret.assert_not_called()
        function_app.DefaultAzureCredential.assert_not_called()

    def test_requests_without_idempotency_key_are_untagged(self):
        with mock.patch.dict(os.environ, {"IDEMPOTENCY_STORAGE_URL": ""}):
            self.assertEqual(self._request_runner().status_code, 202)
        (vm,) = self._bodies()["VirtualMachine"]
        self.assertFalse(hasattr(vm, "tags"))
        self.compute.virtual_machines.get.assert_not_called()
        function_app.requests.request.assert_not_called()

    def test_keyed_requests_fail_closed_without_valid_storage_url(self):
        for url in ("", "https://[invalid/runner-requests", "https://host/", "http://host/container"):
            with self.subTest(url=url), mock.patch.dict(os.environ, {"IDEMPOTENCY_STORAGE_URL": url}):
                function_app._runner_config.cache_clear()
                response = self._request_runner({"Idempotency-Key": "k"})
                self.assertEqual(response.status_code, 503)
                self.assertEqual(json.loads(response.body)["missing"], ["IDEMPOTENCY_STORAGE_URL"])
        function_app.DefaultAzureCredential.assert_not_called()

    def test_invalid_or_unavailable_records_stop_before_provisioning(self):
        for upstream_status, record, expected_status in (
            (200, ValueError("private response"), 502),
            (200, {"fingerprint": "invalid"}, 502),
            (403, {}, 503),
        ):
            with self.subTest(upstream_status=upstream_status, record=type(record)):
                stored = mock.Mock(status_code=upstream_status)
                if isinstance(record, ValueError):
                    stored.json.side_effect = record
                else:
                    stored.json.return_value = record
                with mock.patch.object(function_app.requests, "request", return_value=stored):
                    result = self._request_runner({"Idempotency-Key": "k"})
                self.assertEqual(result.status_code, expected_status)
                self.assertNotIn("cleanup-required", json.loads(result.body))
                self.assertNotIn("private response", result.body)
        self.secrets.get_secret.assert_not_called()
        self.resources.resource_groups.get.assert_not_called()

    def test_invalid_idempotency_keys_are_rejected_before_dependencies(self):
        for key in ("", "bad key", "line\nbreak", "x" * 129, "ü"):
            with self.subTest(key=key):
                response = self._request_runner({"Idempotency-Key": key})
                self.assertEqual(response.status_code, 400)
        self.secrets.get_secret.assert_not_called()
        self.resources.resource_groups.get.assert_not_called()

    def _vm_exists(self, tags):
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        self.compute.virtual_machines.get.side_effect = None
        self.compute.virtual_machines.get.return_value = types.SimpleNamespace(
            tags=tags
        )

    def test_same_key_and_body_create_one_vm_and_return_the_same_resources(self):
        key = {"Idempotency-Key": "run-42-1-hv"}
        self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
        first = self._request_runner(key)
        self.assertEqual(first.status_code, 202)
        (vm,) = self._bodies()["VirtualMachine"]
        self.assertRegex(vm.tags[function_app.VM_REQUEST_TAG], r"^[a-f0-9]{64}$")

        self._vm_exists(vm.tags)
        function_app.requests.request.reset_mock()
        with self.assertLogs(level="INFO") as logs, mock.patch.dict(
            os.environ, {"VNET_NAME": "changed-vnet"}
        ):
            second = self._request_runner(key)

        first_body, second_body = json.loads(first.body), json.loads(second.body)
        self.assertEqual(second.status_code, 202)
        self.assertTrue(second_body["replayed"])
        self.assertNotIn("replayed", first_body)
        self.assertEqual(first_body["resources"], second_body["resources"])
        self.assertEqual(second_body["status-url"], "/api/runner_status")
        self.assertIn('"event": "replayed"', str(logs.output))
        # The replay neither fetches a new registration token nor writes to Azure.
        self.compute.virtual_machines.begin_create_or_update.assert_called_once()
        self.resources.resource_groups.create_or_update.assert_called_once()
        self.assertEqual(self.secrets.get_secret.call_count, 2)
        self.assertEqual(function_app.requests.post.call_count, 1)
        self.assertEqual(
            [call.args[0] for call in function_app.requests.request.call_args_list],
            ["GET"],
        )

    def test_concurrent_requests_share_one_provisioning_slot_across_invocations(self):
        key = {"Idempotency-Key": "concurrent"}
        provisioning, resume = Event(), Event()
        vm_put = self.compute.virtual_machines.begin_create_or_update

        def pause_provisioning(*args, **kwargs):
            provisioning.set()
            if not resume.wait(timeout=5):
                raise AssertionError("Test did not release provisioning")

        vm_put.side_effect = pause_provisioning
        with ThreadPoolExecutor(max_workers=2) as pool:
            first = pool.submit(self._request_runner, key)
            try:
                self.assertTrue(provisioning.wait(timeout=5))
                duplicate = pool.submit(self._request_runner, key).result(timeout=5)
                self.assertEqual(duplicate.status_code, 503)
                self.assertEqual(duplicate.headers["Retry-After"], "5")
                self.assertNotIn("cleanup-required", json.loads(duplicate.body))
                conflict = self._request_runner(key, **{"resource-group": "gh-runner-tmp-other"})
                self.assertEqual(conflict.status_code, 409)
                self.assertEqual(function_app.requests.post.call_count, 1)
                vm_put.assert_called_once()
            finally:
                resume.set()
            accepted = first.result(timeout=5)

        retry = self._request_runner(key)
        self.assertEqual(accepted.status_code, 202)
        self.assertEqual(retry.status_code, 202)
        self.assertEqual(json.loads(accepted.body)["resources"], json.loads(retry.body)["resources"])
        vm_put.assert_called_once()

    def test_lost_lease_stops_before_arm_writes(self):
        for existing in (False, True):
            with self.subTest(existing=existing):
                if existing:
                    self.resources.resource_groups.get.side_effect = None
                    self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
                    self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
                self.storage.reject_renewal = True
                response = self._request_runner({"Idempotency-Key": f"k-{existing}"})
                self.assertEqual(response.status_code, 503)
                self.assertEqual(response.headers["Retry-After"], "5")
                self.assertNotIn("cleanup-required", json.loads(response.body))
        self.resources.resource_groups.create_or_update.assert_not_called()
        self.network.virtual_networks.begin_create_or_update.assert_not_called()
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()
        function_app.requests.post.assert_not_called()

    def test_guard_survives_lease_expiry_during_arm_write(self):
        key = {"Idempotency-Key": "expired"}
        group_put = self.resources.resource_groups.create_or_update
        second_responses = []

        def expire_lease(*args):
            with self.storage.lock:
                self.storage.leases.clear()
            second_responses.append(self._request_runner(key))

        group_put.side_effect = expire_lease
        with mock.patch.object(function_app, "time", return_value=1000) as clock:
            first = self._request_runner(key)
            self.assertEqual(first.status_code, 503)
            self.assertTrue(json.loads(first.body)["cleanup-required"])
            self.assertNotIn("Retry-After", first.headers)
            (second,) = second_responses
            self.assertEqual(second.status_code, 503)
            self.assertEqual(second.headers["Retry-After"], "5")
            self.assertNotIn("cleanup-required", json.loads(second.body))
            group_put.assert_called_once()
            function_app.requests.post.assert_not_called()
            self.network.virtual_networks.begin_create_or_update.assert_not_called()

            clock.return_value += function_app._IN_PROGRESS_SECONDS
            self.resources.resource_groups.get.side_effect = None
            self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
            self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
            recovered = self._request_runner(key)
        self.assertEqual(recovered.status_code, 202)
        self.assertEqual(json.loads(first.body)["resources"], json.loads(recovered.body)["resources"])
        group_put.assert_called_once()

    def test_acceptance_committed_before_storage_timeout_is_replayed(self):
        def timeout_after_accept(*args, **kwargs):
            response = self.storage.request(*args, **kwargs)
            if kwargs.get("json", {}).get("accepted"):
                raise Timeout("response was lost")
            return response

        key = {"Idempotency-Key": "accepted"}
        function_app.requests.request.side_effect = timeout_after_accept
        first = self._request_runner(key)
        self.assertEqual(first.status_code, 503)
        self.assertTrue(json.loads(first.body)["cleanup-required"])
        replay = self._request_runner(key)
        self.assertEqual(replay.status_code, 202)
        self.assertTrue(json.loads(replay.body)["replayed"])
        self.assertEqual(json.loads(first.body)["resources"], json.loads(replay.body)["resources"])
        function_app.requests.post.assert_called_once()
        self.compute.virtual_machines.begin_create_or_update.assert_called_once()

    def test_retries_wait_for_an_accepted_vm_that_is_not_yet_visible(self):
        key = {"Idempotency-Key": "k"}
        vm_put = self.compute.virtual_machines.begin_create_or_update
        vm_put.side_effect = ServiceResponseError("read timeout")
        with mock.patch.object(function_app, "time", return_value=1000):
            first = self._request_runner(key)
            self.resources.resource_groups.get.side_effect = None
            self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
            self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
            pending = self._request_runner(key)
            self.assertEqual(pending.status_code, 503)
            self.assertEqual(pending.headers["Retry-After"], "5")
            self.assertNotIn("cleanup-required", json.loads(pending.body))
            names = json.loads(first.body)["resources"]
            self.compute.virtual_machines.get.assert_called_once_with(
                "gh-runner-tmp-1-hv", names["vm"]
            )
            self._vm_exists(vm_put.call_args.args[-1].tags)
            retry = self._request_runner(key)

        self.assertEqual(first.status_code, 503)
        self.assertEqual(retry.status_code, 202)
        self.assertTrue(json.loads(retry.body)["replayed"])
        self.assertEqual(names, json.loads(retry.body)["resources"])
        vm_put.assert_called_once()
        self.network.virtual_networks.begin_create_or_update.assert_called_once()
        function_app.requests.post.assert_called_once()

    def test_retry_does_not_repeat_an_unobservable_network_write(self):
        key = {"Idempotency-Key": "delayed-vnet"}
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
        vnet = self.network.virtual_networks
        vnet.begin_create_or_update.side_effect = ServiceResponseError("read timeout")
        with mock.patch.object(function_app, "time", return_value=1000) as clock:
            first = self._request_runner(key)
            self.assertEqual(first.status_code, 503)
            self.assertTrue(json.loads(first.body)["cleanup-required"])
            function_app.requests.post.assert_not_called()
            pending = self._request_runner(key)
            self.assertEqual(pending.status_code, 503)
            self.assertEqual(pending.headers["Retry-After"], "5")
            self.assertNotIn("cleanup-required", json.loads(pending.body))
            vnet.begin_create_or_update.assert_called_once()
            self.compute.virtual_machines.begin_create_or_update.assert_not_called()
            function_app.requests.post.assert_not_called()

            clock.return_value += function_app._IN_PROGRESS_SECONDS
            vnet.get.side_effect = None
            vnet.get.return_value.provisioning_state = "Succeeded"
            resumed = self._request_runner(key)
        self.assertEqual(resumed.status_code, 202)
        names = json.loads(first.body)["resources"]
        self.assertEqual(json.loads(resumed.body)["resources"], names)
        vnet.begin_create_or_update.assert_called_once()
        self.assertEqual(vnet.get.call_args.args, ("gh-runner-tmp-1-hv", names["vnet"]))
        self.compute.virtual_machines.begin_create_or_update.assert_called_once()
        function_app.requests.post.assert_called_once()

    def test_failure_before_an_arm_write_allows_an_immediate_keyed_retry(self):
        key = {"Idempotency-Key": "pre-write-failure"}
        self.secrets.get_secret.side_effect = ServiceRequestError()
        with mock.patch.object(function_app, "time", return_value=1000):
            first = self._request_runner(key)
            self.assertEqual(first.status_code, 503)
            self.assertEqual(first.headers["Retry-After"], "5")
            self.assertNotIn("cleanup-required", json.loads(first.body))
            self.resources.resource_groups.create_or_update.assert_not_called()
            function_app.requests.post.assert_not_called()
            self.secrets.get_secret.side_effect = None
            self.assertEqual(self._request_runner(key).status_code, 202)
        self.compute.virtual_machines.begin_create_or_update.assert_called_once()
        function_app.requests.post.assert_called_once()

    def test_keyed_spot_capacity_rejection_can_fall_back_to_regular_vm(self):
        vm_put = self.compute.virtual_machines.begin_create_or_update
        vm_put.side_effect = [HttpResponseError(status_code=409, code="SkuNotAvailable"), mock.DEFAULT]
        with mock.patch.dict(os.environ, {"VM_USE_SPOT": "true"}):
            response = self._request_runner({"Idempotency-Key": "spot-capacity"})
        self.assertEqual(response.status_code, 202)
        spot, regular = vm_put.call_args_list
        self.assertEqual(spot.args[:2], regular.args[:2])
        self.assertEqual(spot.args[-1].priority, "Spot")
        self.assertFalse(hasattr(regular.args[-1], "priority"))
        for call in (spot, regular):
            self.assertEqual(call.kwargs["match_condition"], function_app.MatchConditions.IfMissing)

    def test_retry_after_timeout_with_the_same_key_reuses_the_resource_names(self):
        key = {"Idempotency-Key": "run-42-1-hv"}
        vm_put = self.compute.virtual_machines.begin_create_or_update
        vnet_put = self.network.virtual_networks.begin_create_or_update
        self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
        vm_put.side_effect = ServiceResponseError("read timeout")
        with (
            self.assertLogs(level="ERROR"),
            mock.patch.object(function_app, "time", return_value=1000),
        ):
            first = self._request_runner(key)
        first_body = json.loads(first.body)
        self.assertEqual(first.status_code, 503)
        self.assertTrue(first_body["cleanup-required"])
        self.assertIn("Idempotency-Key", first_body["message"])
        self.assertNotIn("Retry-After", first.headers)

        # The VM write did not land; the resource group did.
        vm_put.side_effect = None
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        self.network.virtual_networks.get.side_effect = None
        self.network.virtual_networks.get.return_value.provisioning_state = "Succeeded"
        with mock.patch.object(
            function_app, "time", return_value=1000 + function_app._IN_PROGRESS_SECONDS
        ):
            second = self._request_runner(key)

        self.assertEqual(second.status_code, 202)
        self.assertEqual(json.loads(second.body)["resources"], first_body["resources"])
        self.assertEqual(
            [call.args[1] for call in vm_put.call_args_list],
            [first_body["vm"]] * 2,
        )
        vnet_put.assert_called_once()
        self.assertEqual(vnet_put.call_args.args[1], first_body["resources"]["vnet"])
        self.resources.resource_groups.create_or_update.assert_called_once()
        self.assertEqual(function_app.requests.post.call_count, 2)

    def test_recovery_waits_for_an_existing_network_write(self):
        key = {"Idempotency-Key": "pending-network"}
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
        vnet = self.network.virtual_networks
        poller = vnet.begin_create_or_update.return_value
        poller.done.return_value = False
        with mock.patch.object(function_app, "time", return_value=1000) as clock:
            self.assertEqual(self._request_runner(key).status_code, 504)
            clock.return_value += function_app._IN_PROGRESS_SECONDS
            vnet.get.side_effect = None
            vnet.get.return_value.provisioning_state = "Updating"
            token_calls = function_app.requests.post.call_count
            for _ in range(2):
                response = self._request_runner(key)
                self.assertEqual(response.status_code, 503)
                self.assertEqual(response.headers["Retry-After"], "5")
                self.assertEqual(json.loads(response.body)["stage"], "network")
                self.assertNotIn("cleanup-required", json.loads(response.body))
                self.assertEqual(function_app.requests.post.call_count, token_calls)
        function_app.requests.post.assert_not_called()
        vnet.begin_create_or_update.assert_called_once()
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()

    def test_checks_claim_and_budget_after_network_recovery_before_token_post(self):
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
        for operations in (
            self.network.virtual_networks,
            self.network.subnets,
            self.network.public_ip_addresses,
            self.network.network_security_groups,
        ):
            operations.get.side_effect = None
            operations.get.return_value.provisioning_state = "Succeeded"

        for case, status in (("lease", 503), ("budget", 504)):
            with self.subTest(case=case), mock.patch.object(
                function_app, "monotonic", return_value=0
            ) as clock:
                def check_nic(*args):
                    self.storage.reject_renewal = case == "lease"
                    if case == "budget":
                        clock.return_value = 110
                    return types.SimpleNamespace(provisioning_state="Succeeded", id="nic")

                self.network.network_interfaces.get.side_effect = check_nic
                response = self._request_runner({"Idempotency-Key": case})
                self.assertEqual(response.status_code, status)
                self.assertEqual(response.headers["Retry-After"], "5")
                self.assertNotIn("cleanup-required", json.loads(response.body))
                function_app.requests.post.assert_not_called()
                self.compute.virtual_machines.begin_create_or_update.assert_not_called()

    def test_same_key_with_any_different_request_field_conflicts(self):
        key = {"Idempotency-Key": "conflict"}
        with mock.patch.dict(os.environ, {"ALLOWED_REPOS": "owner/repo,owner/other"}):
            self.assertEqual(self._request_runner(key).status_code, 202)
            for overrides in (
                {"repo": "owner/other"},
                {"runner-name": "other-name"},
                {"resource-group": "gh-runner-tmp-other"},
                {"virtualization-flavor": "virtualbox"},
            ):
                with self.subTest(overrides=overrides):
                    conflict = self._request_runner(key, **overrides)
                    self.assertEqual(conflict.status_code, 409)
                    self.assertNotIn("cleanup-required", json.loads(conflict.body))
            replay = self._request_runner(key, repo="OWNER/REPO")
            self.assertEqual(replay.status_code, 202)
            self.assertTrue(json.loads(replay.body)["replayed"])
        function_app.requests.post.assert_called_once()
        self.compute.virtual_machines.begin_create_or_update.assert_called_once()

    def test_different_keys_get_different_resource_names(self):
        self.compute.virtual_machines.get.side_effect = ResourceNotFoundError()
        first = json.loads(self._request_runner({"Idempotency-Key": "a"}).body)
        second = json.loads(self._request_runner({"Idempotency-Key": "b"}).body)
        for resource in ("vm", "vnet", "ip", "nic", "nsg"):
            self.assertNotEqual(first["resources"][resource], second["resources"][resource])

    def test_preexisting_vm_without_matching_fingerprint_conflicts(self):
        key = {"Idempotency-Key": "run-42-1-hv"}
        for tags in ({function_app.VM_REQUEST_TAG: "0" * 64}, None, {}):
            with self.subTest(tags=tags):
                self._vm_exists(tags)
                with self.assertLogs(level="ERROR"):
                    response = self._request_runner(key, **{"runner-name": "other"})
                body = json.loads(response.body)
                self.assertEqual(response.status_code, 409)
                self.assertIn("different request", body["message"])
                self.assertNotIn("cleanup-required", body)
        self.secrets.get_secret.assert_not_called()
        self.compute.virtual_machines.begin_create_or_update.assert_not_called()
        self.resources.resource_groups.create_or_update.assert_not_called()

    def test_idempotency_lookup_refuses_foreign_resource_groups(self):
        self.resources.resource_groups.get.side_effect = None
        self.resources.resource_groups.get.return_value = _resource_group({})
        with self.assertLogs(level="ERROR"):
            response = self._request_runner({"Idempotency-Key": "k"})
        self.assertEqual(response.status_code, 409)
        self.compute.virtual_machines.get.assert_not_called()
        self.secrets.get_secret.assert_not_called()

    def test_unexpected_error_logs_frames_without_message(self):
        self.network.virtual_networks.begin_create_or_update.side_effect = (
            AttributeError("private-secret")
        )
        with self.assertLogs(level="ERROR") as logs:
            response = self._request_runner()
        self.assertEqual(response.status_code, 500)
        self.assertNotIn("frames", json.loads(response.body))
        self.assertNotIn("function_app.py:", response.body)
        event = json.loads(logs.records[-1].getMessage())
        self.assertEqual(event["exception"], "AttributeError")
        self.assertTrue(
            any(frame.startswith("function_app.py:") for frame in event["frames"])
        )
        self.assertNotIn("private-secret", str(logs.output) + response.body)

    def test_azure_errors_log_upstream_status(self):
        self.network.subnets.begin_create_or_update.side_effect = HttpResponseError(
            "private-secret", 400, "InvalidRequestContent"
        )
        with self.assertLogs(level="ERROR") as logs:
            self.assertEqual(self._request_runner().status_code, 502)
        event = json.loads(logs.records[-1].getMessage())
        self.assertEqual(event["upstream-status"], 400)
        self.assertEqual(event["arm-code"], "InvalidRequestContent")
        self.assertNotIn("frames", event)

    def test_github_redirect_is_explained(self):
        self.github.status_code = 301
        response = self._request_runner()
        self.assertEqual(response.status_code, 502)
        self.assertIn("redirected", json.loads(response.body)["message"])

    def test_sdk_clients_get_bounded_retry_policies(self):
        self.assertEqual(self._request_runner().status_code, 202)
        policies = []
        for client in (
            function_app.ResourceManagementClient,
            function_app.NetworkManagementClient,
            function_app.ComputeManagementClient,
        ):
            kwargs = client.call_args.kwargs
            self.assertIsInstance(
                kwargs["retry_policy"], function_app._BoundedRetryPolicy
            )
            self.assertEqual(kwargs["retry_policy"].kwargs["retry_total"], 2)
            policies.append(kwargs["retry_policy"])
        # Pipeline policies are linked to one pipeline and must not be shared.
        self.assertEqual(len({id(policy) for policy in policies}), 3)
        self.assertEqual(function_app.SecretClient.call_args.kwargs["retry_total"], 0)
        self.assertEqual(
            function_app.DefaultAzureCredential.call_args.kwargs["retry_total"], 2
        )

    def test_retry_after_is_capped(self):
        policy = function_app._BoundedRetryPolicy()
        self.assertEqual(policy.get_retry_after(600), 2)
        self.assertEqual(policy.get_retry_after(1), 1)
        self.assertIsNone(policy.get_retry_after(None))


class RunnerStatusTest(unittest.TestCase):
    def setUp(self):
        self.compute = mock.Mock()
        self.resources = mock.Mock()
        self.resources.resource_groups.get.return_value = _resource_group(OWNED_TAGS)
        function_app._runner_config.cache_clear()
        self.addCleanup(function_app._runner_config.cache_clear)
        for patch in (
            mock.patch.object(
                function_app, "ComputeManagementClient", return_value=self.compute
            ),
            mock.patch.object(
                function_app, "ResourceManagementClient", return_value=self.resources
            ),
            mock.patch.object(function_app, "DefaultAzureCredential"),
            mock.patch.dict(os.environ, REQUEST_RUNNER_ENV),
        ):
            patch.start()
            self.addCleanup(patch.stop)

    def _status(self, state="Creating", codes=()):
        self.compute.virtual_machines.get.return_value = types.SimpleNamespace(
            provisioning_state=state,
            instance_view=types.SimpleNamespace(
                statuses=[
                    types.SimpleNamespace(code=code, message="private-secret")
                    for code in codes
                ]
            ),
        )
        return function_app.handle_runner_status(
            _request({"resource-group": "gh-runner-tmp-ci", "vm": "runner-123"})
        )

    def test_observes_late_provisioning_failure_after_acceptance(self):
        response = self._status()
        self.assertEqual(response.status_code, 202)
        self.assertEqual(json.loads(response.body)["status"], "provisioning")
        self.assertEqual(response.headers["Retry-After"], "10")
        with self.assertLogs(level="ERROR") as logs:
            response = self._status("Failed", ("ProvisioningState/failed",))
        self.assertEqual(response.status_code, 200)
        self.assertEqual(json.loads(response.body)["status"], "failed")
        self.assertIn("ProvisioningState/failed", str(logs.output))
        self.assertNotIn("private-secret", response.body + str(logs.output))
        self.compute.virtual_machines.get.assert_called_with(
            "gh-runner-tmp-ci", "runner-123", expand="instanceView"
        )

    def test_success_and_eviction_after_success(self):
        response = self._status("Succeeded", ("PowerState/running",))
        self.assertEqual(response.status_code, 200)
        self.assertEqual(json.loads(response.body)["status"], "succeeded")
        for code in (
            "PowerState/deallocated",
            "PowerState/stopped",
            "ProvisioningState/failed",
        ):
            with self.subTest(code=code):
                self.assertEqual(
                    json.loads(self._status("Succeeded", (code,)).body)["status"],
                    "failed",
                )

    def test_stopped_vm_during_initial_creation_is_still_provisioning(self):
        response = self._status("Creating", ("PowerState/stopped",))
        self.assertEqual(response.status_code, 202)
        self.assertEqual(json.loads(response.body)["status"], "provisioning")

    def test_failed_and_canceled_without_instance_view(self):
        for state in ("Failed", "Canceled", "Cancelled"):
            self.assertEqual(json.loads(self._status(state).body)["status"], "failed")
        self.compute.virtual_machines.get.return_value = types.SimpleNamespace(
            provisioning_state="Succeeded", instance_view=None
        )
        response = function_app.handle_runner_status(
            _request({"resource-group": "gh-runner-tmp-ci", "vm": "runner"})
        )
        self.assertEqual(json.loads(response.body)["status"], "succeeded")

    def test_missing_vm_or_group(self):
        for operation in (
            self.resources.resource_groups.get,
            self.compute.virtual_machines.get,
        ):
            with self.subTest(operation=operation):
                operation.side_effect = ResourceNotFoundError()
                response = self._status()
                self.assertEqual(response.status_code, 404)
                self.assertEqual(json.loads(response.body)["status"], "not-found")
                operation.side_effect = None

    def test_ownership_is_checked_before_reading_vm(self):
        self.resources.resource_groups.get.return_value = _resource_group({})
        self.assertEqual(self._status().status_code, 403)
        self.compute.virtual_machines.get.assert_not_called()

    def test_malformed_input_and_names(self):
        for body in (
            {},
            [],
            {"resource-group": "production", "vm": "runner"},
            {"resource-group": "gh-runner-tmp-ci", "vm": "../runner"},
        ):
            response = function_app.handle_runner_status(_request(body))
            self.assertEqual(response.status_code, 400)
        self.resources.resource_groups.get.assert_not_called()

    def test_status_query_failure_is_mapped_and_code_is_logged(self):
        self.compute.virtual_machines.get.side_effect = HttpResponseError(
            "private-secret", 500, "InternalOperationError"
        )
        with self.assertLogs(level="ERROR") as logs:
            response = self._status()
        self.assertEqual(response.status_code, 503)
        self.assertIn("InternalOperationError", str(logs.output))
        self.assertNotIn("private-secret", response.body + str(logs.output))

    def test_status_authentication(self):
        with mock.patch.object(
            function_app, "require_authenticated_caller", return_value=(False, "denied")
        ):
            self.assertEqual(function_app.runner_status(_request({})).status_code, 401)
        self.resources.resource_groups.get.assert_not_called()

    def test_deleting_group_or_vm_stops_polling(self):
        response = self._status("Deleting")
        self.assertEqual(response.status_code, 200)
        self.assertEqual(json.loads(response.body)["status"], "deleting")
        self.resources.resource_groups.get.return_value = _resource_group(
            OWNED_TAGS, "Deleting"
        )
        self.compute.virtual_machines.get.reset_mock()
        response = function_app.handle_runner_status(
            _request({"resource-group": "gh-runner-tmp-ci", "vm": "runner-123"})
        )
        self.assertEqual(json.loads(response.body)["status"], "deleting")
        self.compute.virtual_machines.get.assert_not_called()

    def test_status_only_requires_subscription_configuration(self):
        with mock.patch.dict(os.environ, {"VAULT_URL": "", "SUBSCRIPTION_ID": ""}):
            response = self._status()
        self.assertEqual(response.status_code, 503)
        self.assertEqual(json.loads(response.body)["missing"], ["SUBSCRIPTION_ID"])


if __name__ == "__main__":
    unittest.main()
