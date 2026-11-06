"""Exercise runner safety/cleanup with fake tools, without touching libvirt."""
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("gateway-guest-access-linux.sh").resolve()
TOOLS = r'''#!/usr/bin/env python3
import json, os, pathlib, signal, socket, subprocess, sys, time
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
state = pathlib.Path(os.environ["FAKE_STATE"])
mode = os.environ["FAKE_MODE"]
def record(message):
    with (state / "calls").open("a") as log: log.write(message + "\n")
if name == "sudo":
    if args[0] == "virsh": sys.exit(subprocess.call(args))
    sys.exit(0)
if name == "mktemp":
    # Keep every simulated runner file in the test temporary directory.
    directory = __import__("tempfile").mkdtemp(dir=state)
    print(directory)
elif name == "virsh":
    if "list" in args:
        if mode == "existing": print("unrelated-vm")
        elif (state / "domain").exists(): print("ubuntu-server-amd64-dev-alchemy")
    elif "dominfo" in args: sys.exit(0 if (state / "domain").exists() else 1)
    elif "domblklist" in args:
        disk = (state / "disk").read_text()
        if mode == "foreign": disk = "/unrelated/vm.qcow2"
        print("file disk vda " + disk)
    elif "net-info" in args: print("Active: yes")
elif name == "go":
    if args[0] == "build":
        output = pathlib.Path(args[args.index("-o") + 1])
        output.symlink_to(state / "bin" / output.name)
    elif args[0] == "test":
        disk = pathlib.Path(os.environ["DEV_ALCHEMY_LIBVIRT_IMAGE_DIR"]) / "ubuntu-server-amd64-dev-alchemy.qcow2"
        disk.touch()
        (state / "disk").write_text(str(disk))
        if mode != "partial": (state / "domain").touch()
        print("simulated guest test result")
        sys.exit(0 if mode == "success" else 1)
elif name == "sailwright":
    record(" ".join(args))
    if args[0] == "destroy":
        (state / "domain").unlink(missing_ok=True)
        if (state / "disk").exists(): pathlib.Path((state / "disk").read_text()).unlink(missing_ok=True)
elif name == "sailwright-gateway":
    root = pathlib.Path(args[args.index("--state-dir") + 1])
    agent = pathlib.Path(os.environ["SAILWRIGHT_GATEWAY_DIR"])
    agent.mkdir(parents=True, exist_ok=True)
    params = {"os":"ubuntu", "type":"server", "arch":"amd64", "address":"192.168.122.2", "port":22}
    events = [{"event":"vm_info"}, {"event":"tunnel_opened", "params":params},
              {"event":"tunnel_closed", "params":dict(params, bytes_to_vm=10, bytes_from_vm=20)}]
    (root / "audit.jsonl").write_text("\n".join(json.dumps(e) for e in events))
    sock = socket.socket(socket.AF_UNIX)
    sock.bind(str(agent / "gateway.sock"))
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    while True: time.sleep(.1)
'''


class GuestRunnerTest(unittest.TestCase):
    def run_runner(self, mode):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        state = pathlib.Path(temporary.name)
        binaries = state / "bin"
        binaries.mkdir()
        fake = binaries / "fake-tool"
        fake.write_text(TOOLS)
        fake.chmod(0o755)
        # Standard hosted runners need not include ripgrep. Never let a missing
        # optional tool turn the existing-VM safety check into a false negative.
        (binaries / "rg").write_text("#!/bin/sh\nexit 127\n")
        (binaries / "rg").chmod(0o755)
        for tool in ("sudo", "mktemp", "virsh", "git", "go", "sailwright", "sailwright-gateway", "qemu-system-x86_64"):
            (binaries / tool).symlink_to(fake)
        # The script expects the repository-relative audit verifier.
        workspace = state / "workspace"
        (workspace / "scripts/ci").mkdir(parents=True)
        shutil.copy(SCRIPT.with_name("verify-gateway-guest-audit.py"), workspace / "scripts/ci")
        env = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                   GITHUB_ACTIONS="true", RUNNER_ENVIRONMENT="github-hosted",
                   RUNNER_TEMP=str(state), GITHUB_WORKSPACE=str(workspace),
                   GITHUB_REPOSITORY_OWNER="example", FAKE_STATE=str(state), FAKE_MODE=mode)
        result = subprocess.run(["bash", str(SCRIPT)], env=env, cwd=workspace,
                                capture_output=True, text=True, timeout=20)
        calls = (state / "calls").read_text() if (state / "calls").exists() else ""
        return result, state, workspace / "artifact/gateway-guest-access", calls

    def test_existing_vm_prevents_all_lifecycle_commands(self):
        result, _, _, calls = self.run_runner("existing")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("existing VMs", result.stderr)
        self.assertEqual(calls, "")

    def test_failed_guest_test_cleans_only_owned_target_and_stays_failed(self):
        for mode in ("failed", "partial"):
            with self.subTest(mode=mode):
                result, state, artifacts, calls = self.run_runner(mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("destroy ubuntu --type server --arch amd64", calls)
                self.assertFalse((state / "domain").exists())
                self.assertFalse(pathlib.Path((state / "disk").read_text()).exists())
                self.assertIn("PASS: no domains or managed disks remain", (artifacts / "cleanup.txt").read_text())
                self.assertIn("PASS:", (artifacts / "audit-check.txt").read_text())

    def test_success_preserves_success_after_cleanup_and_evidence(self):
        result, _, artifacts, calls = self.run_runner("success")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("destroy ubuntu --type server --arch amd64", calls)
        self.assertIn("PASS:", (artifacts / "audit-check.txt").read_text())

    def test_foreign_disk_is_preserved_and_cleanup_fails(self):
        result, state, artifacts, calls = self.run_runner("foreign")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("destroy", calls)
        self.assertTrue((state / "domain").exists())
        self.assertIn("Refusing fallback cleanup", (artifacts / "cleanup.txt").read_text())


if __name__ == "__main__":
    unittest.main()
