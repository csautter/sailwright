"""Contract tests for the ARM request bodies sent by request_runner.

Unlike test_function_app.py, these run against the real, pinned Azure SDKs and
check the JSON each SDK puts on the wire, so a wrong model field name or a
serialization change in an SDK upgrade fails here instead of in production.
The file name keeps them out of the default discovery pattern, which loads the
SDK stubs; run them in their own process with requirements.txt installed:

    python -m unittest discover -s scripts/gh-runner-func -p 'contract_test*.py'
"""

import importlib.metadata
import json
import os
import sys
import unittest
from unittest import mock

try:
    importlib.metadata.version("azure-mgmt-network")
    SDKS_INSTALLED = True
except importlib.metadata.PackageNotFoundError:
    SDKS_INSTALLED = False

if SDKS_INSTALLED:
    import azure.functions as func
    from azure.core.credentials import AccessToken
    from azure.core.pipeline.transport import HttpResponse, HttpTransport
    from azure.keyvault.secrets import SecretClient
    from azure.mgmt.compute import ComputeManagementClient
    from azure.mgmt.compute._utils.model_base import SdkJSONEncoder as ComputeEncoder
    from azure.mgmt.network import NetworkManagementClient
    from azure.mgmt.network._utils.model_base import SdkJSONEncoder as NetworkEncoder
    from azure.mgmt.resource.resources import ResourceManagementClient
    from azure.mgmt.resource.resources._utils.model_base import (
        SdkJSONEncoder as ResourceEncoder,
    )

    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import function_app


if SDKS_INSTALLED:

    class _Response(HttpResponse):
        def __init__(self, request, status, body, headers=None):
            super().__init__(request, None)
            self.status_code = status
            self.headers = {"Content-Type": "application/json", **(headers or {})}
            self.content_type = "application/json"
            self.reason = "mock ARM response"
            self._body = json.dumps(body).encode()

        def body(self):
            return self._body

        def read(self):
            return self._body

        def json(self):
            return json.loads(self._body)

        def iter_bytes(self):
            yield self._body

        def iter_raw(self):
            yield self._body

    class _Transport(HttpTransport):
        def __init__(self, responses):
            self.responses = iter(responses)
            self.requests = []

        def open(self):
            pass

        def close(self):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *args):
            self.close()

        def send(self, request, **kwargs):
            self.requests.append((request.method, request.url, kwargs))
            status, body, headers = next(self.responses)
            return _Response(request, status, body, headers)


ENV = {
    "ALLOWED_TENANT_ID": "tenant",
    "ALLOWED_AUDIENCE": "api://broker",
    "VAULT_URL": "https://vault",
    "SUBSCRIPTION_ID": "sub",
    "LOCATION": "westeurope",
    "VM_NAME": "gh-runner-vm",
    "VM_SIZE": "Standard_D4s_v5",
    "ADMIN_USERNAME": "runneradmin",
    "CUSTOM_IMAGE_ID": "/images/gh-runner",
    "RESOURCE_GROUP": "unused",
}

NETWORK_OPERATIONS = (
    "virtual_networks",
    "subnets",
    "public_ip_addresses",
    "network_security_groups",
    "network_interfaces",
)

REGULAR_VM_PROPERTIES = {
    "hardwareProfile": {"vmSize": "Standard_D4s_v5"},
    "storageProfile": {
        "imageReference": {"id": "/images/gh-runner-hyperv"},
        "osDisk": {
            "createOption": "FromImage",
            "managedDisk": {"storageAccountType": "Premium_LRS"},
        },
    },
    "osProfile": {
        "computerName": "1-hv",
        "adminUsername": "runneradmin",
        "adminPassword": "secret",
    },
    "networkProfile": {"networkInterfaces": [{"id": "/id/network_interfaces"}]},
}


@unittest.skipUnless(SDKS_INSTALLED, "requires the Azure SDKs from requirements.txt")
class RequestRunnerWireFormatTest(unittest.TestCase):
    def setUp(self):
        self.network = mock.Mock()
        for operation in NETWORK_OPERATIONS:
            poller = getattr(
                self.network, operation
            ).begin_create_or_update.return_value
            poller.result.return_value.id = f"/id/{operation}"
        self.compute = mock.Mock()
        self.resources = mock.Mock()
        secrets = mock.Mock()
        secrets.get_secret.return_value.value = "secret"
        github = mock.Mock(status_code=201)
        github.json.return_value = {"token": "registration-token"}
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
            mock.patch.object(function_app.requests, "post", return_value=github),
            mock.patch.dict(os.environ, ENV),
        ]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)

    def _request_runner(self):
        body = {
            "repo": "owner/repo",
            "resource-group": "gh-runner-tmp-1-hv",
            "runner-name": "1-hv",
        }
        request = func.HttpRequest(
            method="POST", url="/api/request_runner", body=json.dumps(body).encode()
        )
        return function_app.handle_request_runner(request)

    @staticmethod
    def _wire(operation, encoder):
        # Serialize the body exactly as the SDK operation does before sending it.
        return json.loads(
            json.dumps(operation.call_args.args[-1], cls=encoder, exclude_readonly=True)
        )

    def _vm_wire(self):
        bodies = []
        for call in self.compute.virtual_machines.begin_create_or_update.call_args_list:
            body = json.loads(
                json.dumps(call.args[-1], cls=ComputeEncoder, exclude_readonly=True)
            )
            # The bootstrap script is covered elsewhere; only its presence matters here.
            self.assertTrue(body["properties"]["osProfile"].pop("customData"))
            bodies.append(body)
        return bodies

    def test_resource_group_and_network_bodies(self):
        self.assertEqual(self._request_runner().status_code, 202)

        self.assertEqual(
            self._wire(
                self.resources.resource_groups.create_or_update, ResourceEncoder
            ),
            {"location": "westeurope", "tags": {"managed-by": "gh-runner-broker"}},
        )
        expected = {
            "virtual_networks": {
                "location": "westeurope",
                "properties": {"addressSpace": {"addressPrefixes": ["10.0.0.0/16"]}},
            },
            "subnets": {"properties": {"addressPrefix": "10.0.0.0/24"}},
            "public_ip_addresses": {
                "location": "westeurope",
                "sku": {"name": "Standard"},
                "properties": {"publicIPAllocationMethod": "Static"},
            },
            "network_security_groups": {"location": "westeurope"},
            "network_interfaces": {
                "location": "westeurope",
                "properties": {
                    "ipConfigurations": [
                        {
                            "name": "ipconfig1",
                            "properties": {
                                "subnet": {"id": "/id/subnets"},
                                "publicIPAddress": {"id": "/id/public_ip_addresses"},
                            },
                        }
                    ],
                    "networkSecurityGroup": {"id": "/id/network_security_groups"},
                },
            },
        }
        for operation, body in expected.items():
            with self.subTest(operation=operation):
                self.assertEqual(
                    self._wire(
                        getattr(self.network, operation).begin_create_or_update,
                        NetworkEncoder,
                    ),
                    body,
                )

    def test_regular_vm_body(self):
        self.assertEqual(self._request_runner().status_code, 202)

        self.assertEqual(
            self._vm_wire(),
            [{"location": "westeurope", "properties": REGULAR_VM_PROPERTIES}],
        )

    def test_spot_vm_and_fallback_bodies(self):
        self.compute.virtual_machines.begin_create_or_update.side_effect = [
            function_app.HttpResponseError("no capacity"),
            mock.DEFAULT,
        ]

        with (
            mock.patch.dict(os.environ, {"VM_USE_SPOT": "true"}),
            self.assertLogs(level="WARNING"),
        ):
            self.assertEqual(self._request_runner().status_code, 202)

        spot_properties = {
            **REGULAR_VM_PROPERTIES,
            "priority": "Spot",
            "evictionPolicy": "Deallocate",
            "billingProfile": {"maxPrice": -1},
        }
        self.assertEqual(
            self._vm_wire(),
            [
                {"location": "westeurope", "properties": spot_properties},
                {"location": "westeurope", "properties": REGULAR_VM_PROPERTIES},
            ],
        )

    def test_accepted_vm_is_observed_as_failed_using_real_sdk_transport(self):
        # No poller thread survives the HTTP handler. A subsequent authenticated
        # status call reads ARM, even on a different Function worker.
        transport = _Transport(
            [
                (
                    201,
                    {"properties": {"provisioningState": "Creating"}},
                    {
                        "Azure-AsyncOperation": "https://management.azure.com/mock-operation"
                    },
                ),
                (
                    200,
                    {
                        "properties": {
                            "provisioningState": "Failed",
                            "instanceView": {
                                "statuses": [
                                    {
                                        "code": "ProvisioningState/failed",
                                        "message": "private-secret",
                                    }
                                ]
                            },
                        }
                    },
                    {},
                ),
            ]
        )
        credential = mock.Mock(spec=["get_token"])
        credential.get_token.return_value = AccessToken("fake-token", 9999999999)
        compute = ComputeManagementClient(
            credential, "sub", transport=transport, **function_app._SDK_OPTIONS
        )
        self.addCleanup(compute.close)
        self.resources.resource_groups.get.return_value.tags = {
            "managed-by": "gh-runner-broker"
        }
        with mock.patch.object(
            function_app, "ComputeManagementClient", return_value=compute
        ):
            created = self._request_runner()
            self.assertEqual(created.status_code, 202)
            self.assertEqual(len(transport.requests), 1)
            body = json.loads(created.get_body())
            self.assertEqual(body["status-url"], "/api/runner_status")
            request = func.HttpRequest(
                method="POST",
                url="/api/runner_status",
                body=json.dumps(
                    {
                        "resource-group": body["resource-group"],
                        "vm": body["vm"],
                    }
                ).encode(),
            )
            response = function_app.handle_runner_status(request)
        self.assertEqual(response.status_code, 200)
        self.assertEqual(json.loads(response.get_body())["status"], "failed")
        self.assertEqual([item[0] for item in transport.requests], ["PUT", "GET"])
        self.assertIn("$expand=instanceView", transport.requests[-1][1])
        self.assertNotIn("private-secret", response.get_body().decode())

    def test_sdk_clients_recognize_transport_timeouts(self):
        credential = mock.Mock(spec=["get_token"])
        for factory, args in (
            (ComputeManagementClient, (credential, "sub")),
            (NetworkManagementClient, (credential, "sub")),
            (ResourceManagementClient, (credential, "sub")),
            (SecretClient, ("https://vault.vault.azure.net", credential)),
        ):
            with self.subTest(client=factory.__name__):
                with factory(*args, **function_app._SDK_OPTIONS) as client:
                    pipeline_client = (
                        client._client._client
                        if factory is SecretClient
                        else client._client
                    )
                    transport = pipeline_client._pipeline._transport
                    self.assertEqual(transport.connection_config.timeout, 3)
                    self.assertEqual(transport.connection_config.read_timeout, 5)


if __name__ == "__main__":
    unittest.main()
