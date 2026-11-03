"""Check installer failures using executables that reproduce Ansible output."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class AnsibleCoreCheckTest(unittest.TestCase):
    def check_version(self, output, exit_code=0):
        with tempfile.TemporaryDirectory() as directory:
            executable = Path(directory) / "ansible-playbook"
            executable.write_text(f"#!/bin/sh\ncat <<'OUTPUT'\n{output}\nOUTPUT\nexit {exit_code}\n")
            executable.chmod(0o755)
            return subprocess.run(
                ["bash", "-c", 'source "$1"; check_ansible_core python3',
                 "test", str(ROOT / "scripts/common/ansible-core.sh")],
                env={**os.environ, "PATH": directory + os.pathsep + os.environ["PATH"]},
                capture_output=True, text=True, check=False,
            )

    def test_accepts_supported_releases(self):
        minimum = (ROOT / "scripts/common/ansible-core-min-version").read_text().strip()
        for version in (minimum, "2.21.5", "3.0.0"):
            with self.subTest(version=version):
                result = self.check_version(f"ansible-playbook [core {version}]")
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_old_unknown_and_prerelease_versions(self):
        for version in ("2.16.3", "2.18.12", "2.19.0rc1", "unknown"):
            with self.subTest(version=version):
                result = self.check_version(f"ansible-playbook [core {version}]")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Sailwright requires ansible-core", result.stderr)
                self.assertIn("PATH", result.stderr)

    def test_reports_startup_failure(self):
        result = self.check_version("AttributeError: module 'dataclasses' has no attribute '_is_type'", 1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("AttributeError", result.stderr)
        self.assertIn("Rerun sailwright install", result.stderr)
