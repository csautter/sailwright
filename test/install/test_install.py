"""Offline regressions for Unix installer staging and the manual-install example.

Run with: python3 -m unittest discover -s test/install -v
"""

import functools
import hashlib
import http.server
import io
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import unittest


REPO = Path(__file__).resolve().parents[2]


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="sailwright install test ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.release = self.root / "release" / "v1.2.3"
        self.release.mkdir(parents=True)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.target = self.root / "installed"
        self.target.mkdir()
        self.work = self.root / "manual"
        self.work.mkdir()
        checksums = []
        for system in ("linux", "darwin"):
            for arch in ("amd64", "arm64"):
                for name in ("sailwright", "sailwright-gateway"):
                    asset = self.release / f"{name}_1.2.3_{system}_{arch}.tar.gz"
                    with tarfile.open(asset, "w:gz") as archive:
                        data = f"#!/bin/sh\necho new-{name}\n".encode()
                        member = tarfile.TarInfo(name)
                        member.size = len(data)
                        member.mode = 0o755
                        archive.addfile(member, io.BytesIO(data))
                    digest = hashlib.sha256(asset.read_bytes()).hexdigest()
                    checksums.append(f"{digest}  dist/{asset.name}")
        self.checksums = self.release / "sailwright_1.2.3_checksums.txt"
        self.checksums.write_text("\n".join(checksums) + "\n")
        for suffix in ("sig", "pem"):
            Path(f"{self.checksums}.{suffix}").write_text("fixture")
        # These tests exercise staging and control flow, not cryptography.
        self.tool("cosign", '#!/bin/sh\nexit "${TEST_COSIGN_EXIT:-0}"\n')
        handler = functools.partial(QuietHandler, directory=str(self.release.parent))
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.01})
        thread.daemon = True
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.env = dict(os.environ)
        # Do not inherit installer settings from the developer's environment.
        for key in list(self.env):
            if key.startswith("SAILWRIGHT_"):
                del self.env[key]
        self.env.update(
            PATH=f"{self.bin}{os.pathsep}{os.environ['PATH']}",
            SAILWRIGHT_VERSION="v1.2.3",
            SAILWRIGHT_DOWNLOAD_URL=f"http://127.0.0.1:{self.server.server_port}",
        )

    def tool(self, name, content):
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o755)

    def fail_for_target(self, command, target, message):
        """Replace command with one that fails only when its last argument is target."""
        real = shutil.which(command)
        self.tool(command, f'''#!/bin/sh
for last; do :; done
case "$last" in
*/{target}) echo '{message}' >&2; exit 1 ;;
esac
exec {real} "$@"
''')

    def install(self, gateway=False):
        args = ["sh", str(REPO / "scripts/install.sh"), "--install-dir", str(self.target)]
        if not gateway:
            args.append("--no-gateway")
        return subprocess.run(args, env=self.env, capture_output=True, text=True, timeout=15)

    def write_originals(self):
        for name in ("sailwright", "sailwright-gateway"):
            (self.target / name).write_text(f"old-{name}")

    def assert_originals(self):
        for name in ("sailwright", "sailwright-gateway"):
            self.assertEqual((self.target / name).read_text(), f"old-{name}")
        self.assertEqual(sorted(p.name for p in self.target.iterdir()), ["sailwright", "sailwright-gateway"])

    def test_existing_staging_symlink_does_not_overwrite_another_file(self):
        victim = self.root / "unrelated-file"
        victim.write_text("keep this data")
        symlink = self.target / ".sailwright.tmp"
        symlink.symlink_to(victim)

        result = self.install()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(victim.read_text(), "keep this data")
        self.assertTrue(symlink.is_symlink())
        binary = self.target / "sailwright"
        self.assertFalse(binary.is_symlink())
        self.assertEqual(subprocess.check_output([str(binary)], text=True).strip(), "new-sailwright")
        self.assertEqual({p.name for p in self.target.iterdir()}, {"sailwright", ".sailwright.tmp"})

    def test_failed_copy_preserves_original_and_cleans_staging(self):
        (self.target / "sailwright").write_text("old working binary")
        self.tool("cp", "#!/bin/sh\necho 'simulated full disk' >&2\nexit 1\n")

        result = self.install()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated full disk", result.stderr)
        self.assertEqual((self.target / "sailwright").read_text(), "old working binary")
        self.assertEqual([p.name for p in self.target.iterdir()], ["sailwright"])

    def test_failed_promotion_preserves_original_and_cleans_staging(self):
        (self.target / "sailwright").write_text("old working binary")
        self.tool("mv", "#!/bin/sh\necho 'simulated promotion failure' >&2\nexit 1\n")

        result = self.install()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated promotion failure", result.stderr)
        self.assertEqual((self.target / "sailwright").read_text(), "old working binary")
        self.assertEqual([p.name for p in self.target.iterdir()], ["sailwright"])

    def test_update_replaces_both_binaries_and_cleans_staging(self):
        self.write_originals()

        result = self.install(gateway=True)

        self.assertEqual(result.returncode, 0, result.stderr)
        for name in ("sailwright", "sailwright-gateway"):
            self.assertEqual(subprocess.check_output([str(self.target / name)], text=True).strip(), f"new-{name}")
        self.assertEqual(sorted(p.name for p in self.target.iterdir()), ["sailwright", "sailwright-gateway"])

    def test_failed_gateway_staging_preserves_both_originals(self):
        self.write_originals()
        self.fail_for_target("cp", "sailwright-gateway", "simulated full disk")

        result = self.install(gateway=True)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated full disk", result.stderr)
        self.assert_originals()

    def test_failed_gateway_promotion_restores_both_originals(self):
        self.write_originals()
        self.fail_for_target("mv", "sailwright-gateway", "simulated gateway promotion failure")

        result = self.install(gateway=True)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated gateway promotion failure", result.stderr)
        self.assertIn("restored the previous install", result.stderr)
        self.assert_originals()

    def test_failed_first_install_promotion_removes_new_binaries(self):
        self.fail_for_target("mv", "sailwright-gateway", "simulated gateway promotion failure")

        result = self.install(gateway=True)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("restored the previous install", result.stderr)
        self.assertEqual(list(self.target.iterdir()), [])

    def test_unrestorable_backup_is_kept(self):
        self.write_originals()
        # Fail the gateway promotion and the restore of the CLI that follows it.
        real = shutil.which("mv")
        self.tool("mv", f'''#!/bin/sh
for last; do :; done
case "$last" in */sailwright-gateway) echo 'simulated failure' >&2; exit 1 ;; esac
case "$*" in *.old\\ *) echo 'simulated failure' >&2; exit 1 ;; esac
exec {real} "$@"
''')

        result = self.install(gateway=True)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("warning: could not restore", result.stderr)
        self.assertIn("see the warnings above", result.stderr)
        backups = list(self.target.glob(".sailwright-install.*/sailwright.old"))
        self.assertEqual(len(backups), 1)
        self.assertEqual(backups[0].read_text(), "old-sailwright")

    def test_concurrent_installs_use_independent_staging_files(self):
        copy = shutil.which("cp")
        self.tool("cp", f'''#!{sys.executable}
import os, pathlib, subprocess, sys, time
subprocess.run([{copy!r}] + sys.argv[1:], check=True)
root = pathlib.Path(os.environ['TEST_BARRIER_DIR'])
(root / (os.environ['TEST_RUN'] + '.ready')).write_text(sys.argv[-1])
deadline = time.monotonic() + 10
while not (root / 'release').exists():
    if time.monotonic() > deadline:
        sys.exit('timed out waiting for concurrent installer')
    time.sleep(0.01)
''')
        barrier = self.root / "barrier"
        barrier.mkdir()
        processes = []
        try:
            for run in ("first", "second"):
                env = dict(self.env, TEST_BARRIER_DIR=str(barrier), TEST_RUN=run)
                processes.append(subprocess.Popen(
                    ["sh", str(REPO / "scripts/install.sh"), "--install-dir", str(self.target), "--no-gateway"],
                    env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                ))
            deadline = time.monotonic() + 10
            while len(list(barrier.glob("*.ready"))) < 2 and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertEqual(len(list(barrier.glob("*.ready"))), 2)
            staged = [p.read_text() for p in barrier.glob("*.ready")]
            self.assertEqual(len(set(staged)), 2)
            (barrier / "release").touch()
            for process in processes:
                _, stderr = process.communicate(timeout=10)
                self.assertEqual(process.returncode, 0, stderr)
            self.assertEqual([p.name for p in self.target.iterdir()], ["sailwright"])
        finally:
            (barrier / "release").touch()
            for process in processes:
                if process.poll() is None:
                    process.kill()
                process.communicate()

    def manual_install(self):
        if not shutil.which("sha256sum"):
            self.skipTest("The Linux manual-install example requires sha256sum")
        # Execute the actual documented example so its verification ordering
        # and failure behavior cannot silently drift from a copied test script.
        text = (REPO / "docs/installation.md").read_text().split("## Manual install", 1)[1]
        snippet = text.split("```bash\n", 1)[1].split("```", 1)[0]
        self.tool("curl", f'''#!{sys.executable}
import os, pathlib, shutil, sys
url = sys.argv[-1]
if url.endswith('/releases/latest'):
    print('{{"tag_name": "v1.2.3"}}')
else:
    name = url.rsplit('/', 1)[-1]
    shutil.copyfile(pathlib.Path(os.environ['TEST_RELEASE']) / name, name)
''')
        return subprocess.run(
            ["bash", "-c", snippet], cwd=self.work,
            env=dict(self.env, TEST_RELEASE=str(self.release)),
            capture_output=True, text=True, timeout=15,
        )

    def test_manual_example_runs_after_successful_verification(self):
        result = self.manual_install()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("new-sailwright", result.stdout)

    def test_manual_example_does_not_extract_after_checksum_failure(self):
        self.checksums.write_text("0" * 64 + "  dist/sailwright_1.2.3_linux_amd64.tar.gz\n")

        result = self.manual_install()

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FAILED", result.stdout)
        self.assertFalse((self.work / "sailwright").exists())
        self.assertNotIn("new-sailwright", result.stdout)

    def test_manual_example_does_not_extract_after_signature_failure(self):
        self.env["TEST_COSIGN_EXIT"] = "1"

        result = self.manual_install()

        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.work / "sailwright").exists())

    def test_manual_example_does_not_extract_after_download_failure(self):
        Path(f"{self.checksums}.pem").unlink()

        result = self.manual_install()

        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.work / "sailwright").exists())


if __name__ == "__main__":
    unittest.main()
