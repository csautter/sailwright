"""Host-free schema and bootstrap tests using local Git repositories."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from jsonschema import Draft202012Validator

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
import protocol


def example(kind):
    return json.loads((SCRIPTS / "examples" / f"{kind}.json").read_bytes())


class SchemaTests(unittest.TestCase):
    def test_examples_and_schema_drafts(self):
        for kind in ("request", "result"):
            with self.subTest(kind=kind):
                schema = json.loads((SCRIPTS / "schemas" / f"{kind}-v1.json").read_bytes())
                Draft202012Validator.check_schema(schema)
                self.assertEqual(protocol.validate(kind, json.dumps(example(kind)).encode())["schema_version"], 1)

    def test_request_defaults_and_invalid_fields(self):
        request = example("request")
        del request["operation_class"], request["minimum_free_gib"]
        normalized = protocol.validate("request", json.dumps(request).encode())
        self.assertEqual(normalized["operation_class"], "vm-lifecycle")
        self.assertEqual(normalized["minimum_free_gib"], 10)

        for changes, message in [
            ({"commit": "abc123"}, r"\$\.commit"),
            ({"commit": request["commit"] + "\n"}, r"\$\.commit"),
            ({"id": "../escape"}, r"\$\.id"),
            ({"id": "con"}, r"\$\.id"),
            ({"target": {"os": "freebsd", "arch": "amd64"}}, r"\$\.target\.os"),
            ({"repo": "ext::bad"}, "source repo"),
            ({"repo": "https://user:password@example.org/repo.git"}, "without credentials"),
            ({"time_limit_seconds": 0}, r"\$\.time_limit_seconds"),
            ({"cleanup": "keep"}, r"\$\.cleanup"),
            ({"required_tools": ["../tool"]}, r"\$\.required_tools"),
            ({"operation_class": "host-changing"}, "human_approval"),
            ({"human_approval": {"approved_by": "operator", "scope": ["install"]}}, "only allowed"),
        ]:
            with self.subTest(changes=changes), self.assertRaisesRegex(protocol.ProtocolError, message):
                protocol.validate("request", json.dumps({**request, **changes}).encode())

        del request["test_commands"]
        request["scenarios"] = ["vm-lifecycle-linux"]
        protocol.validate("request", json.dumps(request).encode())
        del request["scenarios"]
        with self.assertRaisesRegex(protocol.ProtocolError, "provide scenarios or test_commands"):
            protocol.validate("request", json.dumps(request).encode())

    def test_broken_results(self):
        for field in example("result"):
            result = example("result")
            del result[field]
            with self.subTest(field=field), self.assertRaisesRegex(protocol.ProtocolError, field):
                protocol.validate("result", json.dumps(result).encode())
        result = example("result")
        result["steps"][0]["exit_code"] = 7
        result["cleanup"]["remaining_vms"] = ["sw-left-behind"]
        del result["follow_up"][0]["reason"]
        with self.assertRaisesRegex(protocol.ProtocolError, r"exit_code.*remaining_vms.*reason"):
            protocol.validate("result", json.dumps(result).encode())

    def test_invalid_json_is_rejected_without_values_in_error(self):
        for data in (b'{"key": 1, "key": 2}', b'{"value": NaN}', b'{"value": 1e999}'):
            with self.subTest(data=data), self.assertRaises(protocol.ProtocolError):
                protocol.validate("result", data)
        with self.assertRaisesRegex(protocol.ProtocolError, r"^invalid result:") as raised:
            protocol.validate("result", b'"sensitive-test-value"')
        self.assertNotIn("sensitive-test-value", str(raised.exception))

    def test_host_mismatch(self):
        with patch.object(protocol, "host_facts", return_value=("windows", "arm64")):
            with self.assertRaisesRegex(protocol.ProtocolError, "needs linux/amd64, found windows/arm64"):
                protocol.preflight(example("request"), False)


@unittest.skipIf(os.name == "nt", "fixture uses a POSIX container and shell stubs")
class LocalGitTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="host-agent-test-")))
        self.work = self.root / "work area"
        self.results = self.root / "results Bob's $test & team’s.git"
        self.program = [sys.executable, "-B", str(SCRIPTS / "protocol.py")]
        # Git may use the configured identity but should ignore inherited repo paths.
        self.env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        config = self.root / "gitconfig"
        config.write_text("[user]\n\tname = Test Operator\n\temail = operator@example.org\n", encoding="utf-8")
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        for tool in ("go", "claude"):
            executable = bin_dir / tool
            executable.write_text("#!/bin/sh\nexit 99\n", encoding="utf-8")
            executable.chmod(0o700)
        self.hook = self.root / "sentinel-hook.py"
        self.hook_marker = self.root / "hook-ran"
        self.command_marker = self.root / "request-command-ran"
        self.hook.write_text(f"from pathlib import Path\nPath({str(self.hook_marker)!r}).touch()\n"
                             "raise SystemExit(99)\n", encoding="utf-8")
        self.env.update(
            PATH=os.pathsep.join((str(Path(sys.executable).parent), str(bin_dir), self.env["PATH"])),
            GIT_CONFIG_GLOBAL=str(config), GIT_CONFIG_NOSYSTEM="1",
            SAILWRIGHT_HOST_AGENT_BOOTSTRAP=str(SCRIPTS),
            SAILWRIGHT_HOST_AGENT_PROVIDER="claude",
            SAILWRIGHT_HOST_AGENT_HOOK=str(self.hook),
            SAILWRIGHT_HOST_AGENT_WORK_ROOT=str(self.work),
        )
        self.git("init", "-q", "--bare", str(self.results))
        bare_source = self.root / "source.git"
        self.git("init", "-q", "--bare", str(bare_source))
        source = self.root / "upstream-work"
        self.git("init", "-q", "-b", "main", str(source))
        (source / "fixture.txt").write_text("original\n", encoding="utf-8")
        (source / ".gitattributes").write_text("fixture.txt filter=fixture\n", encoding="utf-8")
        self.git("-C", str(source), "add", ".")
        self.git("-C", str(source), "commit", "-qm", "Initial fixture")
        self.source_sha = self.git("-C", str(source), "rev-parse", "HEAD").strip()
        (source / "fixture.txt").write_text("newer\n", encoding="utf-8")
        self.git("-C", str(source), "commit", "-qam", "Later fixture")
        self.git("-C", str(source), "push", "-q", str(bare_source), "main")
        # A global checkout filter must also stay inert for request-provided trees.
        self.filter_marker = self.root / "filter-ran"
        self.git("config", "--file", str(config), "filter.fixture.smudge",
                 f"touch '{self.filter_marker}'; cat")
        os_name, arch = protocol.host_facts()
        self.request = example("request")
        self.request.update(repo=str(bare_source), commit=self.source_sha,
                            target={"os": os_name, "arch": arch}, required_tools=[],
                            minimum_free_gib=1, test_commands=[f"touch '{self.command_marker}'"])
        del self.request["operation_class"]
        self.request_file = self.root / "request.json"

    def run_cli(self, *command, status=0, env=None):
        result = subprocess.run(command, cwd=self.root, env=self.env if env is None else env,
                                capture_output=True, text=True, timeout=30, check=False)
        self.assertEqual(result.returncode, status,
                         f"{command!r}\nstdout: {result.stdout}\nstderr: {result.stderr}")
        return result

    def git(self, *args):
        return self.run_cli("git", *args).stdout

    def publish(self, prefix="selftest/", status=0):
        self.request_file.write_text(json.dumps(self.request), encoding="utf-8")
        return self.run_cli(*self.program, "request", str(self.request_file),
                            "--results-repo", str(self.results), "--branch-prefix", prefix, status=status)

    def start_posix(self, line, *flags, status=0, shell="bash", env=None):
        return self.run_cli(shell, "-c", " ".join([line, "--dry-run", *flags]), status=status, env=env)

    def assert_ready(self, output, ref):
        directory = Path(output.splitlines()[0].removeprefix("Work directory: "))
        self.assertIn("Dry run: preflight passed for claude with skill sailwright-host-agent.", output)
        for checkout in ("source", "results"):
            self.run_cli("git", "-C", str(directory / checkout), "symbolic-ref", "-q", "HEAD", status=1)
        self.assertEqual(self.git("-C", str(directory / "source"), "rev-parse", "HEAD").strip(), self.source_sha)
        self.assertEqual((directory / "source" / "fixture.txt").read_text(encoding="utf-8"), "original\n")
        request = directory / "results" / "selftest" / self.request["id"] / "request.json"
        self.assertEqual(protocol.validate("request", request.read_bytes())["operation_class"], "vm-lifecycle")
        self.assertEqual(self.git("-C", str(directory / "results"), "rev-parse", "HEAD").strip(),
                         self.git("--git-dir", str(self.results), "rev-parse", ref).strip())
        self.assertEqual(self.git("-C", str(directory / "results"), "remote", "get-url", "origin").strip(),
                         str(self.results))
        for path in (self.hook_marker, self.command_marker, self.filter_marker, self.work / "active"):
            self.assertFalse(path.exists(), str(path))

    def test_bash_publish_and_full_dry_run(self):
        published = self.publish()
        posix, windows = published.stdout.splitlines()
        self.assertEqual(published.stderr, "")
        self.assertTrue(posix.startswith('bash "${SAILWRIGHT_HOST_AGENT_BOOTSTRAP:'))
        self.assertTrue(windows.startswith("& (Join-Path $env:SAILWRIGHT_HOST_AGENT_BOOTSTRAP 'start.ps1')"))
        ref = f"refs/heads/selftest/{self.request['id']}"
        self.assertEqual(self.git("--git-dir", str(self.results), "for-each-ref", "--format=%(refname)").strip(), ref)
        before = self.git("--git-dir", str(self.results), "rev-parse", ref)
        self.assertIn(f"--request-commit {before.strip()}", posix)
        result = self.start_posix(posix)
        self.assert_ready(result.stdout, ref)
        self.assertEqual(self.git("--git-dir", str(self.results), "rev-parse", ref), before)
        duplicate = self.publish(status=1)
        self.assertIn("already exists", duplicate.stderr)

    @unittest.skipUnless(shutil.which("zsh"), "zsh is unavailable")
    def test_zsh_and_custom_branch(self):
        posix, _ = self.publish(prefix="experiments/").stdout.splitlines()
        result = self.start_posix(posix, shell="zsh")
        self.assert_ready(result.stdout, f"refs/heads/experiments/{self.request['id']}")

    @unittest.skipUnless(shutil.which("pwsh"), "PowerShell is unavailable")
    def test_powershell_publish_and_full_dry_run(self):
        _, windows = self.publish().stdout.splitlines()
        result = self.run_cli("pwsh", "-NoProfile", "-NonInteractive", "-Command",
                              windows + " --dry-run; exit $LASTEXITCODE")
        self.assert_ready(result.stdout, f"refs/heads/selftest/{self.request['id']}")

    def test_bash_preflight_errors(self):
        cases = [
            ({"commit": "0" * 40}, "cannot fetch source commit"),
            ({"repo": str(self.root / "missing source")}, "cannot fetch source commit"),
            ({"required_tools": ["missing-sailwright-test-tool"]}, "missing required tool"),
            ({"minimum_free_gib": 2**40}, "insufficient free disk"),
            ({"target": {"os": "windows", "arch": "amd64"}}, "host mismatch"),
        ]
        for index, (changes, error) in enumerate(cases, start=1):
            with self.subTest(changes=changes), patch.dict(self.request, changes):
                self.request["id"] = f"error-{index}"
                posix, _ = self.publish().stdout.splitlines()
                self.assertIn(error, self.start_posix(posix, status=1).stderr)
                self.assertFalse((self.work / "active").exists())

    def test_missing_results_repo_and_git(self):
        line, _ = self.publish().stdout.splitlines()
        self.results.rename(self.root / "unavailable.git")
        self.assertIn("cannot fetch pinned request from results repo", self.start_posix(line, status=1).stderr)
        env = {**self.env, "PATH": str(self.root / "empty")}
        result = self.run_cli(*self.program, "start", "--results-repo", str(self.results),
                              "--id", self.request["id"], "--request-commit", "0" * 40, "--dry-run",
                              status=1, env=env)
        self.assertIn("missing required tool on PATH: git", result.stderr)

    def test_active_lock_and_changed_request(self):
        posix, _ = self.publish().stdout.splitlines()
        lock = self.work / "active"
        lock.mkdir(parents=True)
        self.assertIn("active request lock exists", self.start_posix(posix, status=1).stderr)
        lock.rmdir()
        ref = f"refs/heads/selftest/{self.request['id']}"
        tree = self.git("--git-dir", str(self.results), "rev-parse", ref + "^{tree}").strip()
        moved = self.git("--git-dir", str(self.results), "commit-tree", tree, "-p", ref, "-m", "Moved").strip()
        self.git("--git-dir", str(self.results), "update-ref", ref, moved)
        self.assertIn(f"found {moved}", self.start_posix(posix, status=1).stderr)

    def test_host_changes_require_operator_flag(self):
        self.request.update(operation_class="host-changing",
                            human_approval={"approved_by": "Test Operator", "scope": ["install"]})
        posix, windows = self.publish().stdout.splitlines()
        self.assertNotIn("--approve-host-changes", posix + windows)
        self.assertIn("requires the human operator", self.start_posix(posix, status=1).stderr)
        self.assertIn("Dry run: preflight passed", self.start_posix(posix, "--approve-host-changes").stdout)
        self.assertFalse(self.hook_marker.exists())
        self.assertFalse(self.command_marker.exists())

    def test_launch_contract_with_a_stub(self):
        self.hook.write_text(
            "import json, os, sys\nprint(json.dumps([os.getcwd(), *sys.argv[1:]]))\nraise SystemExit(23)\n",
            encoding="utf-8",
        )
        posix, _ = self.publish().stdout.splitlines()
        result = self.run_cli("bash", "-c", posix, status=23)
        work = Path(result.stdout.splitlines()[0].removeprefix("Work directory: "))
        request = work / "results" / "selftest" / self.request["id"] / "request.json"
        ref = f"refs/heads/selftest/{self.request['id']}"
        pin = self.git("--git-dir", str(self.results), "rev-parse", ref).strip()
        cwd, *arguments = json.loads(result.stdout.splitlines()[-1])
        self.assertEqual(Path(cwd).resolve(), (work / "source").resolve())
        self.assertEqual(arguments, [
            "--provider", "claude", "--skill", "sailwright-host-agent",
            "--request", str(request), "--source-dir", str(work / "source"),
            "--results-dir", str(work / "results"), "--results-ref", ref, "--request-commit", pin,
        ])
        self.assertFalse(self.command_marker.exists())
        self.assertFalse((self.work / "active").exists())


if __name__ == "__main__":
    unittest.main()
