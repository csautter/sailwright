"""Exercise the polling helper against a fake Azure CLI, without cloud access."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class WaitRunnerProvisionedTests(unittest.TestCase):
    def run_poll(self, responses):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sequence = root / "responses.json"
            sequence.write_text(json.dumps(responses))
            calls = root / "calls"
            az = root / "az"
            az.write_text(
                "#!/usr/bin/env python3\n"
                "import json, os, pathlib, sys\n"
                "calls = pathlib.Path(os.environ['FAKE_AZ_CALLS'])\n"
                "index = int(calls.read_text()) if calls.exists() else 0\n"
                "calls.write_text(str(index + 1))\n"
                "responses = json.loads(pathlib.Path(os.environ['FAKE_AZ_RESPONSES']).read_text())\n"
                "code, body = responses[min(index, len(responses) - 1)]\n"
                "print(body, file=sys.stderr if code else sys.stdout)\n"
                "sys.exit(code)\n"
            )
            az.chmod(0o755)
            result = subprocess.run(
                ["bash", str(Path(__file__).with_name("wait-runner-provisioned.sh"))],
                env={
                    **os.environ,
                    "PATH": directory + os.pathsep + os.environ["PATH"],
                    "FUNCTION_APP_NAME": "test-broker",
                    "AZURE_CLIENT_ID": "test-client",
                    "RESOURCE_GROUP": "test-group",
                    "VM_NAME": "test-vm",
                    "PROVISION_POLL_SECONDS": "0",
                    "PROVISION_TIMEOUT_SECONDS": "10",
                    "PROVISION_MAX_ERRORS": "3",
                    "FAKE_AZ_RESPONSES": str(sequence),
                    "FAKE_AZ_CALLS": str(calls),
                },
                capture_output=True,
                text=True,
                timeout=15,
            )
            return result, int(calls.read_text())

    def test_missing_route_fails_on_first_call(self):
        for error in ("ERROR: Not Found", "ERROR: (NotFound)", "ERROR: HTTP 404"):
            with self.subTest(error=error):
                result, calls = self.run_poll([(1, error)])
                self.assertEqual(result.returncode, 1)
                self.assertEqual(calls, 1)
                self.assertIn("Deploy the updated Function App", result.stderr)

    def test_missing_resource_is_distinct_from_missing_route(self):
        result, calls = self.run_poll(
            [(1, 'ERROR: Not Found({"status": "not-found"})')]
        )
        self.assertEqual(result.returncode, 1)
        self.assertEqual(calls, 1)
        self.assertIn("is not-found", result.stderr)
        self.assertNotIn("Deploy", result.stderr)

    def test_provisioning_and_transient_errors_keep_polling(self):
        result, calls = self.run_poll(
            [
                (0, '{"status": "provisioning"}'),
                (1, "ERROR: service unavailable"),
                (0, '{"status": "succeeded"}'),
            ]
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 3)

    def test_transient_errors_are_bounded(self):
        result, calls = self.run_poll([(1, "ERROR: service unavailable")])
        self.assertEqual(result.returncode, 1)
        self.assertEqual(calls, 3)
        self.assertIn("failed 3 times in a row", result.stderr)

    def test_terminal_states_fail(self):
        for status in ("failed", "deleting"):
            with self.subTest(status=status):
                result, calls = self.run_poll([(0, json.dumps({"status": status}))])
                self.assertEqual(result.returncode, 1)
                self.assertEqual(calls, 1)


if __name__ == "__main__":
    unittest.main()
