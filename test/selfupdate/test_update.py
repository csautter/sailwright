"""Exercise installed release executables against a local HTTP release fixture."""
import functools
import hashlib
import http.server
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile
import threading
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
GOOS = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}[platform.system()]
GOARCH = "arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64"
EXE = ".exe" if GOOS == "windows" else ""


class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/releases/latest":
            self.send_response(302)
            self.send_header("Location", "/releases/tag/v1.0.1")
            self.end_headers()
        else:
            super().do_GET()

    def log_message(self, *_):
        pass


class UpdateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.serve = cls.root / "serve"
        cls.serve.mkdir()
        for version in ("1.0.0", "1.0.1"):
            release = cls.serve / "releases" / "download" / ("v" + version)
            release.mkdir(parents=True)
            sums = []
            for binary, package, symbol in (
                ("sailwright", "./cmd/main.go", "github.com/csautter/dev-alchemy/cmd/cmd.version"),
                ("sailwright-gateway", "./cmd/sailwright-gateway", "main.version"),
            ):
                output = cls.root / (binary + "-" + version + EXE)
                subprocess.run(["go", "build", "-ldflags", f"-X {symbol}=v{version}", "-o", str(output), package], cwd=ROOT, check=True)
                name = binary + EXE
                asset = f"{binary}_{version}_{GOOS}_{GOARCH}" + (".zip" if EXE else ".tar.gz")
                if EXE:
                    with zipfile.ZipFile(release / asset, "w") as z:
                        z.write(output, name)
                else:
                    with tarfile.open(release / asset, "w:gz") as t:
                        t.add(output, arcname=name)
                sums.append(hashlib.sha256((release / asset).read_bytes()).hexdigest() + "  dist/" + asset)
            (release / f"sailwright_{version}_checksums.txt").write_text("\n".join(sums) + "\n")
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Handler, directory=str(cls.serve)))
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.url = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join()
        cls.server.server_close()
        cls.temp.cleanup()

    def setUp(self):
        self.work = tempfile.TemporaryDirectory(dir=self.root)
        self.addCleanup(self.work.cleanup)
        self.bin = Path(self.work.name) / "bin"
        self.bin.mkdir()
        self.env = dict(os.environ, SAILWRIGHT_UPDATE_BASE_URL=self.url,
                        SAILWRIGHT_DOWNLOAD_URL=self.url + "/releases/download",
                        DEV_ALCHEMY_CONFIG_DIR=str(Path(self.work.name) / "config"),
                        DEV_ALCHEMY_CACHE_DIR=str(Path(self.work.name) / "cache"))
        # Fixtures are unsigned. Keep system tools available to the installers,
        # but fail explicitly if a machine unexpectedly provides cosign.
        if shutil.which("cosign"):
            self.fail("Run unsigned local fixtures without cosign in PATH")
        if EXE:
            self.env.update(SAILWRIGHT_VERSION="v1.0.0", SAILWRIGHT_INSTALL_DIR=str(self.bin))
            subprocess.run(["pwsh", "-NoProfile", "-File", str(ROOT / "scripts/install.ps1")], env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        else:
            subprocess.run(["sh", str(ROOT / "scripts/install.sh"), "--version", "v1.0.0", "--install-dir", str(self.bin)], env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.cli = self.bin / ("sailwright" + EXE)

    def run_cli(self, *args, code=0):
        p = subprocess.run([str(self.cli), *args], env=self.env, input="", capture_output=True, text=True)
        self.assertEqual(p.returncode, code, p.stdout + p.stderr)
        return p

    def assert_version(self, version):
        self.assertIn(version, self.run_cli("--version").stdout)
        gateway = subprocess.run([str(self.bin / ("sailwright-gateway" + EXE)), "--version"], capture_output=True, text=True, check=True)
        self.assertIn(version, gateway.stdout)

    def test_update_and_explicit_downgrade(self):
        self.assert_version("v1.0.0")
        self.run_cli("update", "--check", code=2)
        self.run_cli("update", code=1)
        self.assert_version("v1.0.0")
        self.assertIn("cosign not found", self.run_cli("update", "--yes").stderr)
        self.assert_version("v1.0.1")
        self.run_cli("update", "--yes")
        self.run_cli("update", "--check")
        self.run_cli("update", "--yes", "--version", "v1.0.0")
        self.assert_version("v1.0.0")

    def test_tamper_and_missing_asset_leave_both_binaries_unchanged(self):
        release = self.serve / "releases/download/v1.0.1"
        asset = next(release.glob("sailwright-gateway_*." + ("zip" if EXE else "gz")))
        original = asset.read_bytes()
        try:
            asset.write_bytes(b"tampered")
            self.assertIn("SHA-256 mismatch", self.run_cli("update", "--yes", code=1).stderr)
            self.assert_version("v1.0.0")
            asset.unlink()
            self.assertIn("404", self.run_cli("update", "--yes", code=1).stderr)
            self.assert_version("v1.0.0")
        finally:
            asset.write_bytes(original)

    def test_no_gateway_and_offline_settings(self):
        self.run_cli("update", "--yes", "--no-gateway")
        self.assertIn("v1.0.1", self.run_cli("--version").stdout)
        self.env["SAILWRIGHT_UPDATE_BASE_URL"] = "http://127.0.0.1:1"
        self.run_cli("update", "--auto-check", "on", "--auto-check-interval", "30m")
        self.assertIn("true (interval 30m0s)", self.run_cli("version").stdout)
        self.env["SAILWRIGHT_NO_UPDATE_CHECK"] = "1"
        self.assertIn("false", self.run_cli("version").stdout)
        self.run_cli("update", "--auto-check", "off")
        self.run_cli("update", "--auto-check-interval", "1s", code=1)
        self.assertFalse((Path(self.work.name) / "cache/update-check.json").exists())

    def test_invalid_settings_can_be_repaired(self):
        config = Path(self.work.name) / "config"
        config.mkdir(parents=True, exist_ok=True)
        (config / "update.json").write_text('{"enabled":true,"interval":"1m"}')
        self.assertIn("invalid settings", self.run_cli("version").stdout)
        self.assertIn("replacing invalid update settings", self.run_cli("update", "--auto-check", "off").stderr)
        self.assertIn("false (interval 1h0m0s)", self.run_cli("version").stdout)

    def test_latest_never_downgrades(self):
        for name in ("sailwright", "sailwright-gateway"):
            shutil.copyfile(self.root / (name + "-1.0.1" + EXE), self.bin / (name + EXE))
        # A local fixture advertises a release older than the installed binary.
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), lambda *a, **kw: OldReleaseHandler(*a, directory=str(self.serve), **kw))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            self.env["SAILWRIGHT_UPDATE_BASE_URL"] = f"http://127.0.0.1:{server.server_port}"
            self.run_cli("update", "--yes")
            self.assert_version("v1.0.1")
        finally:
            server.shutdown()
            thread.join()
            server.server_close()


class OldReleaseHandler(Handler):
    def do_GET(self):
        self.send_response(302)
        self.send_header("Location", "/releases/tag/v1.0.0")
        self.end_headers()


if __name__ == "__main__":
    unittest.main()
