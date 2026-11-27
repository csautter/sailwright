"""Host-free schema and bootstrap tests using local Git repositories."""

from contextlib import redirect_stderr, redirect_stdout
import io
import json
import os
from pathlib import Path
import shlex
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

    def test_libvirt_example_uses_one_uri_and_the_full_domain(self):
        request, result = example("request"), example("result")
        commands = request["test_commands"]
        uri = "qemu:///system"
        name = "sw-linux-lifecycle-a1b2"
        domain = f"ubuntu-server-amd64-{name}-dev-alchemy"
        self.assertEqual(commands, [step["command"] for step in result["steps"]])
        for command, action in zip(commands[2:5], ("create", "start", "destroy"), strict=True):
            self.assertEqual(shlex.split(command), [
                f"DEV_ALCHEMY_LIBVIRT_URI={uri}", "./sailwright", action, "ubuntu",
                "--type", "server", "--arch", "amd64", "--name", name,
            ])
        self.assertEqual(commands[1], commands[-1])
        self.assertIn(f"virsh --connect {uri} list --all --name", commands[1])
        self.assertIn(f'"{domain}"', commands[1])
        self.assertEqual(result["cleanup"]["owned_vms"], [domain])
        self.assertEqual(result["cleanup"]["remaining_vms"], [])
        evidence = result["cleanup"]["evidence"]
        report = (SCRIPTS / "examples" / "report.md").read_text(encoding="utf-8")
        for text in (request["agent_instructions"], evidence, report):
            self.assertIn(uri, text)
            self.assertIn(domain, text)
        cleanup_log = (SCRIPTS / "examples" / "logs" / "cleanup.txt").read_text(encoding="utf-8")
        for phase in ("Before create", "After destroy"):
            self.assertIn(f"{phase}: {domain}\n$ {commands[1]}\n[No domains returned]\nExit code 0", cleanup_log)

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
        self.launcher = self.root / "trusted launcher"
        self.launcher.mkdir()
        self.hook = self.launcher / "sentinel-hook.py"
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
        (source / ".mcp.json").write_text('{"mcpServers": {}}\n', encoding="utf-8")
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

    @unittest.skipUnless(shutil.which("awk"), "awk is unavailable")
    def test_libvirt_example_inventory_requires_success_and_exact_absence(self):
        request, result = example("request"), example("result")
        domain, = result["cleanup"]["owned_vms"]
        bin_dir = self.root / "bin"
        virsh = bin_dir / "virsh"
        virsh.write_text(
            '#!/bin/sh\n[ "$*" = "--connect qemu:///system list --all --name" ] || exit 99\n'
            'printf "%s\\n" "$INVENTORY"\nexit "$VIRSH_STATUS"\n',
            encoding="utf-8",
        )
        virsh.chmod(0o700)
        for inventory, virsh_status, expected in [
            ("", 0, 0),
            (f"{domain}-other", 0, 0),
            (f"unrelated\n{domain}", 0, 1),
            ("", 7, 7),
        ]:
            with self.subTest(inventory=inventory, virsh_status=virsh_status):
                env = {**self.env, "INVENTORY": inventory, "VIRSH_STATUS": str(virsh_status)}
                run = self.run_cli("bash", "-c", request["test_commands"][1], status=expected, env=env)
                self.assertEqual(run.stdout.strip(), inventory)

    def test_launch_contract_with_a_stub(self):
        unrelated = self.root / "upstream-work" / ".git"
        self.assertNotEqual(self.git("--git-dir", str(unrelated), "rev-parse", "HEAD").strip(), self.source_sha)
        env = {**self.env, "GIT_DIR": str(unrelated), "GIT_WORK_TREE": str(unrelated.parent),
               "GIT_INDEX_FILE": str(unrelated / "index"), "GIT_SSH_COMMAND": "operator-ssh"}
        self.hook.write_text(
            "import json, os, subprocess, sys\n"
            "from pathlib import Path\n"
            "args = dict(zip(sys.argv[1::2], sys.argv[2::2]))\n"
            "heads = [subprocess.check_output(['git', '-C', args[key], 'rev-parse', 'HEAD'], text=True).strip()\n"
            "         for key in ('--source-dir', '--results-dir')]\n"
            "print(json.dumps({\n"
            "    'cwd': os.getcwd(), 'args': sys.argv[1:], 'heads': heads,\n"
            "    'source_config': (Path(args['--source-dir']) / '.mcp.json').is_file(),\n"
            "    'startup_config': Path('.mcp.json').exists(),\n"
            "    'git_env': {key: value for key, value in os.environ.items() if key.startswith('GIT_')},\n"
            "}))\n"
            "raise SystemExit(23)\n",
            encoding="utf-8",
        )
        posix, _ = self.publish().stdout.splitlines()
        result = self.run_cli("bash", "-c", posix, status=23, env=env)
        work = Path(result.stdout.splitlines()[0].removeprefix("Work directory: "))
        request = work / "results" / "selftest" / self.request["id"] / "request.json"
        ref = f"refs/heads/selftest/{self.request['id']}"
        pin = self.git("--git-dir", str(self.results), "rev-parse", ref).strip()
        received = json.loads(result.stdout.splitlines()[-1])
        self.assertEqual(Path(received["cwd"]).resolve(), self.launcher.resolve())
        self.assertTrue(received["source_config"])
        self.assertFalse(received["startup_config"])
        self.assertEqual(received["heads"], [self.source_sha, pin])
        self.assertEqual(received["git_env"], {
            key: env[key] for key in ("GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_SSH_COMMAND")
        })
        self.assertEqual(received["args"], [
            "--provider", "claude", "--skill", "sailwright-host-agent",
            "--request", str(request), "--source-dir", str(work / "source"),
            "--results-dir", str(work / "results"), "--results-ref", ref, "--request-commit", pin,
        ])
        self.assertFalse(self.command_marker.exists())
        self.assertFalse((self.work / "active").exists())

    def test_signaled_hook_retains_lock_until_manual_recovery(self):
        self.hook.write_text("import os, signal\nos.kill(os.getpid(), signal.SIGTERM)\n", encoding="utf-8")
        posix, _ = self.publish().stdout.splitlines()
        result = self.run_cli("bash", "-c", posix, status=1)
        lock = self.work / "active"
        self.assertTrue(lock.is_dir())
        self.assertIn("launch hook terminated by signal", result.stderr)
        self.assertIn(f"active request lock retained at {lock}", result.stderr)
        self.assertIn("finish cleanup before removing the lock directory", result.stderr)
        blocked = self.start_posix(posix, status=1)
        self.assertIn(f"active request lock exists at {lock}", blocked.stderr)
        self.assertEqual(blocked.stdout, "")
        lock.rmdir()
        self.assert_ready(self.start_posix(posix).stdout, f"refs/heads/selftest/{self.request['id']}")

    def test_windows_abnormal_exit_retains_lock_and_ordinary_exit_releases_lock(self):
        posix, _ = self.publish().stdout.splitlines()
        ref = f"refs/heads/selftest/{self.request['id']}"
        pin = self.git("--git-dir", str(self.results), "rev-parse", ref).strip()
        lock = self.work / "active"
        real_popen = subprocess.Popen

        def run_windows_start(injected_status):
            launched = []

            class FakeHookProcess:
                def wait(self):
                    return injected_status

            def fake_popen(command, *args, **kwargs):
                if len(command) >= 3 and command[2] == str(self.hook):
                    launched.append((command, kwargs.get("cwd")))
                    return FakeHookProcess()
                return real_popen(command, *args, **kwargs)

            argv = [
                str(SCRIPTS / "protocol.py"), "start",
                "--results-repo", str(self.results),
                "--branch-prefix", "selftest/",
                "--id", self.request["id"],
                "--request-commit", pin,
            ]
            stdout, stderr = io.StringIO(), io.StringIO()
            with (
                patch.object(protocol, "is_windows", return_value=True),
                patch.object(protocol.subprocess, "Popen", side_effect=fake_popen),
                patch.object(sys, "argv", argv),
                patch.dict(os.environ, self.env, clear=True),
                redirect_stdout(stdout),
                redirect_stderr(stderr),
            ):
                exit_code = protocol.main()
            self.assertEqual(len(launched), 1)
            self.assertEqual(Path(launched[0][1]).resolve(), self.launcher.resolve())
            return exit_code, stdout.getvalue(), stderr.getvalue()

        for status, expected_code in [
            (0x80000003, "0x80000003"),
            (0xC0000005, "0xc0000005"),
            (256, "0x00000100"),
            (-1, "0xffffffff"),
        ]:
            with self.subTest(abnormal_status=status):
                exit_code, stdout, stderr = run_windows_start(status)
                self.assertEqual(exit_code, 1)
                self.assertIn(f"Ready: source {self.source_sha}, results {ref} at {pin}", stdout)
                self.assertTrue(lock.is_dir())
                self.assertIn(f"abnormal Windows exit status {expected_code}", stderr)
                self.assertIn(f"active request lock retained at {lock}", stderr)
                self.assertIn("finish cleanup before removing the lock directory", stderr)
                blocked = self.start_posix(posix, status=1)
                self.assertIn(f"active request lock exists at {lock}", blocked.stderr)
                self.assertEqual(blocked.stdout, "")
                lock.rmdir()

        for status in (23, 0, 255):
            with self.subTest(explicit_status=status):
                exit_code, stdout, stderr = run_windows_start(status)
                self.assertEqual(exit_code, status)
                self.assertIn(f"Ready: source {self.source_sha}, results {ref} at {pin}", stdout)
                self.assertEqual(stderr, "")
                self.assertFalse(lock.exists())

        self.assert_ready(self.start_posix(posix).stdout, ref)

    def test_rejects_unsafe_initial_results_before_hook(self):
        for relative in ("result.json", "report.md", "logs"):
            with self.subTest(path=relative):
                self.request["id"] = "unsafe-" + relative.split(".")[0]
                self.publish()
                request_id = self.request["id"]
                ref = f"refs/heads/selftest/{request_id}"
                stage = self.root / request_id
                self.git("init", "-q", str(stage))
                self.git("-C", str(stage), "fetch", "-q", str(self.results), ref)
                self.git("-C", str(stage), "checkout", "-q", "--detach", "FETCH_HEAD")
                output = stage / "selftest" / request_id / relative
                outside = self.root / f"outside-{request_id}"
                output.symlink_to(outside)
                self.git("-C", str(stage), "add", "--", str(output))
                self.git("-C", str(stage), "commit", "-qm", "Unsafe output path fixture")
                self.git("-C", str(stage), "push", "-q", str(self.results), f"HEAD:{ref}")
                pin = self.git("--git-dir", str(self.results), "rev-parse", ref).strip()
                posix, _ = protocol.start_lines(str(self.results), "selftest/", request_id, pin)
                result = self.run_cli("bash", "-c", posix, status=1)
                self.assertIn("initial results tree must contain only the regular file", result.stderr)
                work = Path(result.stdout.splitlines()[0].removeprefix("Work directory: "))
                self.assertFalse((work / "results" / "selftest").exists())
                self.assertFalse((work / "source").exists())
                self.assertFalse(self.hook_marker.exists())
                self.assertFalse(outside.exists())
                self.assertFalse((self.work / "active").exists())


if __name__ == "__main__":
    unittest.main()
