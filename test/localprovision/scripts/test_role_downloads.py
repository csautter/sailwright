"""Exercise real role install blocks against local archives, without sudo."""

import copy
import functools
import hashlib
import http.server
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import threading
import unittest


@unittest.skipUnless(
    os.environ.get("SAILWRIGHT_LOCAL_INTEGRATION") == "1"
    and shutil.which("ansible-playbook"),
    "set SAILWRIGHT_LOCAL_INTEGRATION=1 with Ansible installed",
)
class RoleDownloadsTest(unittest.TestCase):
    def setUp(self):
        import yaml

        self.yaml = yaml
        self.repo = Path(__file__).resolve().parents[3]
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=self.tmp.name)
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join)
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.url = f"http://127.0.0.1:{self.server.server_port}"

    def archive(self, filename, member):
        path = self.root / filename
        with tarfile.open(path, "w:gz") as archive:
            data = b"#!/bin/sh\necho fixture\n"
            info = tarfile.TarInfo(member)
            info.mode = 0o755
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        return path

    def install_task(self, role, name):
        tasks = self.yaml.safe_load((self.repo / f"roles/{role}/tasks/main.yml").read_text())
        linux = next(task for task in tasks if task["name"].endswith("on Linux"))
        install = copy.deepcopy(next(task for task in linux["block"] if task["name"] == name))

        def sandbox(task):
            task.pop("become", None)
            if "ansible.builtin.tempfile" in task:
                task["ansible.builtin.tempfile"]["path"] = str(self.root)
            if "ansible.builtin.unarchive" in task and role == "k9s":
                task["ansible.builtin.unarchive"]["dest"] = str(self.root / "bin")
            if "ansible.builtin.get_url" in task and role == "k9s":
                args = task["ansible.builtin.get_url"]
                prefix = "https://github.com/derailed/k9s/releases/download/v{{ k9s_version }}"
                for key in ("url", "checksum"):
                    args[key] = args[key].replace(prefix, self.url)
            for key in ("block", "rescue", "always"):
                for child in task.get(key, []):
                    sandbox(child)

        sandbox(install)
        return install

    def run_tasks(self, tasks, variables, success=True):
        play = [{"hosts": "localhost", "gather_facts": False, "vars": variables, "tasks": tasks}]
        path = self.root / "play.yml"
        path.write_text(self.yaml.safe_dump(play))
        result = subprocess.run(
            ["ansible-playbook", "-i", "localhost,", "-c", "local", str(path)],
            cwd=self.repo, capture_output=True, text=True, timeout=60,
            env={**os.environ, "ANSIBLE_NOCOLOR": "1"},
        )
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result.stdout

    def toolbox(self, archive, success, fail_promotion=False):
        target = self.root / "toolbox"
        target.mkdir(exist_ok=True)
        (target / "previous-install").write_text("preserve me")
        task = self.install_task("jetbrains", "Install the Toolbox release")
        if fail_promotion:
            download = task["block"][1]
            promote = next(t for t in download["block"] if t["name"] == "Promote the staged Toolbox installation")
            promote["block"][0]["ansible.builtin.command"] = {"argv": ["/usr/bin/false"]}
        variables = {
            "toolbox_directory": str(target), "toolbox_build": "fixture",
            "toolbox_marker": {"stat": {"exists": False}},
            "toolbox_download": {
                "link": f"{self.url}/{archive.name}",
                "checksumLink": f"{self.url}/checksum.txt",
            },
        }
        (self.root / "checksum.txt").write_text(hashlib.sha256(archive.read_bytes()).hexdigest())
        self.run_tasks([task], variables, success)
        self.assertEqual((target / "previous-install").exists(), not success)
        self.assertEqual((target / ".sailwright-build-fixture").exists(), success)
        self.assertFalse(list(self.root.glob(".sailwright-toolbox-*")))
        if success:
            variables["toolbox_marker"]["stat"]["exists"] = True
            output = self.run_tasks([task], variables)
            self.assertIn("changed=0", output)

    def test_toolbox_bad_archive_preserves_previous_install(self):
        archive = self.root / "invalid.tar.gz"
        archive.write_text("not a tar archive")
        self.toolbox(archive, success=False)

    def test_toolbox_missing_binary_preserves_previous_install(self):
        self.toolbox(self.archive("missing.tar.gz", "toolbox/README"), success=False)

    def test_toolbox_upgrade_and_idempotency(self):
        self.toolbox(self.archive("toolbox.tar.gz", "toolbox/bin/jetbrains-toolbox"), success=True)

    def test_toolbox_failed_promotion_restores_previous_install(self):
        self.toolbox(
            self.archive("toolbox.tar.gz", "toolbox/bin/jetbrains-toolbox"),
            success=False, fail_promotion=True,
        )

    def test_k9s_checksum_failure_and_verified_install(self):
        archive = self.archive("k9s_Linux_amd64.tar.gz", "k9s")
        checksums = self.root / "checksums.sha256"
        (self.root / "bin").mkdir()
        task = self.install_task("k9s", "Install the pinned k9s release")
        variables = {"k9s_version": "0.51.0", "k9s_arch": "amd64", "k9s_installed_version": ""}
        checksums.write_text(f"{'0' * 64}  {archive.name}\n")
        self.run_tasks([task], variables, success=False)
        self.assertFalse((self.root / "bin/k9s").exists())
        checksums.write_text(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
        self.run_tasks([task], variables)
        self.assertTrue((self.root / "bin/k9s").is_file())
        variables["k9s_installed_version"] = "0.51.0"
        self.assertIn("changed=0", self.run_tasks([task], variables))
        self.assertFalse(list(self.root.glob("ansible.*k9s")))
