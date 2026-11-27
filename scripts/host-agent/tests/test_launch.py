"""Host-free guardrail, template, and end-to-end dry-run tests for launch.py."""

import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest

SCRIPTS = Path(__file__).resolve().parents[1]
REPO_ROOT = SCRIPTS.parents[1]
sys.path.insert(0, str(SCRIPTS))
import launch  # noqa: E402
import protocol  # noqa: E402


FAKE_PROVIDER_SCRIPT = r'''#!/usr/bin/env python3
"""Fake Claude/Codex CLI used by host-free launch hook tests."""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time


def extract(prompt, label):
    pattern = rf"- {re.escape(label)}: `([^`]+)`"
    match = re.search(pattern, prompt)
    if not match:
        raise SystemExit(f"missing {label} in prompt")
    return match.group(1)


def main():
    argv = sys.argv[1:]
    behavior = os.environ.get("FAKE_PROVIDER_BEHAVIOR", "pass")
    record_file = os.environ.get("FAKE_PROVIDER_RECORD")
    if record_file:
        codex_home = os.environ.get("CODEX_HOME")
        codex_rules = None
        codex_cfg = None
        if codex_home:
            rules_path = Path(codex_home) / "rules" / "host-agent.rules"
            cfg_path = Path(codex_home) / "config.toml"
            if rules_path.is_file():
                codex_rules = rules_path.read_text(encoding="utf-8")
            if cfg_path.is_file():
                codex_cfg = cfg_path.read_text(encoding="utf-8")
        claude_settings = None
        if "--settings" in argv:
            settings_path = Path(argv[argv.index("--settings") + 1])
            if settings_path.is_file():
                claude_settings = json.loads(settings_path.read_text(encoding="utf-8"))
        Path(record_file).write_text(json.dumps({
            "exe": Path(sys.argv[0]).name,
            "cwd": os.getcwd(),
            "argv": argv,
            "claude_settings": claude_settings,
            "codex_home": codex_home,
            "codex_cfg": codex_cfg,
            "codex_rules": codex_rules,
        }, indent=2), encoding="utf-8")

    if behavior == "sleep":
        time.sleep(10)
        return 0

    prompt = argv[-1]
    request_path = Path(extract(prompt, "Pinned request JSON"))
    source_dir = Path(extract(prompt, "Detached source checkout (untrusted code under test)"))
    results_dir = Path(extract(prompt, "Detached results checkout"))
    results_ref = extract(prompt, "Results Git ref (push target)")
    request_commit = extract(prompt, "Pinned request commit SHA")
    protocol_path = Path(extract(prompt, "Trusted protocol validator"))

    request = json.loads(request_path.read_text(encoding="utf-8"))
    req_id = request["id"]
    source_sha = subprocess.check_output(
        ["git", "-C", str(source_dir), "rev-parse", "HEAD"], text=True
    ).strip()
    assert source_sha == request["commit"], f"SHA mismatch {source_sha} != {request['commit']}"

    result_dir = results_dir / "selftest" / req_id
    logs_dir = result_dir / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)

    domain = f"ubuntu-server-amd64-sw-{req_id}-a1b2-dev-alchemy"
    cleanup_log = (
        f"Before create: {domain}\n"
        f"$ virsh --connect qemu:///system list --all --name\n"
        f"[No domains returned]\n"
        f"Exit code 0\n\n"
        f"After destroy: {domain}\n"
        f"$ virsh --connect qemu:///system list --all --name\n"
        f"[No domains returned]\n"
        f"Exit code 0\n"
    )
    if behavior == "private_key_leak":
        cleanup_log += "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
    (logs_dir / "cleanup.txt").write_text(cleanup_log, encoding="utf-8")

    binary_sha = hashlib.sha256(f"fake-sailwright-{source_sha}".encode("utf-8")).hexdigest()
    overall_status = "failed" if behavior == "report_failed" else "passed"
    step_status = "failed" if behavior == "report_failed" else "passed"
    step_exit = 2 if behavior == "report_failed" else 0

    result_doc = {
        "schema_version": 1,
        "id": req_id,
        "request_commit": request_commit,
        "commit": source_sha,
        "status": overall_status,
        "host": {
            "os": request["target"]["os"],
            "os_version": "Dry-Run Fixture OS 1.0",
            "arch": request["target"]["arch"],
            "hypervisors": [{"name": "libvirt", "version": "12.0.0"}],
        },
        "binary_sha256": binary_sha,
        "steps": [
            {
                "name": "build Sailwright (dry-run fixture)",
                "status": "passed",
                "exit_code": 0,
                "command": "go build -o sailwright ./cmd",
                "duration_seconds": 1.2,
            },
            {
                "name": "execute fixture lifecycle check",
                "status": step_status,
                "exit_code": step_exit,
                "command": f"DEV_ALCHEMY_LIBVIRT_URI=qemu:///system ./sailwright create ubuntu --name sw-{req_id}-a1b2",
                "duration_seconds": 0.4,
            },
        ],
        "cleanup": {
            "status": "verified",
            "owned_vms": [domain],
            "remaining_vms": [],
            "evidence": (
                f"Pre-create and post-destroy inventories in logs/cleanup.txt both succeeded on "
                f"qemu:///system and confirmed {domain} was absent."
            ),
        },
        "open_items": [] if overall_status == "passed" else ["Inspect failing fixture lifecycle step."],
        "follow_up": [
            {
                "kind": "scenario_proposal",
                "proposal": "Capture pre-create and post-destroy hypervisor inventory in the lifecycle scenario.",
                "reason": "Keeps cleanup verification deterministic across runs.",
            }
        ],
    }

    if behavior == "bad_commit":
        result_doc["commit"] = "f" * 40
    if behavior == "invalid_schema":
        del result_doc["follow_up"]

    result_file = result_dir / "result.json"
    result_file.write_text(json.dumps(result_doc, indent=2) + "\n", encoding="utf-8")

    report_file = result_dir / "report.md"
    if behavior != "missing_report":
        report_file.write_text(
            f"# Host test report: {req_id}\n\n"
            f"- Source commit: `{source_sha}`\n"
            f"- Binary SHA-256: `{binary_sha}`\n"
            f"- Cleanup evidence: [logs/cleanup.txt](logs/cleanup.txt)\n",
            encoding="utf-8",
        )

    if behavior == "tamper_request":
        request_path.write_text(request_path.read_text(encoding="utf-8") + " ", encoding="utf-8")

    if behavior == "outside_file":
        (results_dir / "stray.txt").write_text("outside selftest/<id>/\n", encoding="utf-8")

    if behavior == "symlink_log":
        (logs_dir / "link.txt").symlink_to(request_path)

    if behavior not in {"invalid_schema", "bad_commit", "tamper_request", "missing_report", "private_key_leak", "symlink_log"}:
        subprocess.run(
            [sys.executable, "-B", str(protocol_path), "validate", "result", str(result_file)],
            check=True,
        )

    if behavior == "uncommitted":
        return 0

    subprocess.run(["git", "-C", str(results_dir), "add", "-A"], check=True)
    subprocess.run(
        ["git", "-C", str(results_dir), "commit", "--quiet", "-m", f"Add host test result for {req_id}"],
        check=True,
    )

    if behavior == "unpushed":
        return 0

    subprocess.run(
        ["git", "-C", str(results_dir), "push", "--quiet", "origin", f"HEAD:{results_ref}"],
        check=True,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
'''


class TemplateGuardrailTests(unittest.TestCase):
    def test_default_claude_template_passes_and_blocks_required_commands(self):
        settings = json.loads(launch.DEFAULT_CLAUDE_SETTINGS.read_text(encoding="utf-8"))
        validated = launch.validate_claude_settings(settings)
        self.assertEqual(validated["enableAllProjectMcpServers"], False)
        self.assertEqual(validated["disableAllHooks"], True)
        self.assertIn(validated["permissions"]["defaultMode"], launch.ALLOWED_CLAUDE_MODES)
        self.assertNotIn("bypassPermissions", validated["permissions"]["defaultMode"])
        self.assertNotIn("dontAsk", validated["permissions"]["defaultMode"])

        # Confirm Claude auto mode is also accepted when all guardrails stay intact.
        auto_variant = copy.deepcopy(settings)
        auto_variant["permissions"]["defaultMode"] = "auto"
        launch.validate_claude_settings(auto_variant)

    def test_claude_template_rejects_bypass_and_tampering(self):
        base = json.loads(launch.DEFAULT_CLAUDE_SETTINGS.read_text(encoding="utf-8"))

        for mode in ("bypassPermissions", "dontAsk", "yolo"):
            tampered = copy.deepcopy(base)
            tampered["permissions"]["defaultMode"] = mode
            with self.subTest(defaultMode=mode), self.assertRaisesRegex(launch.LaunchError, "defaultMode"):
                launch.validate_claude_settings(tampered)

        tampered = copy.deepcopy(base)
        tampered["enableAllProjectMcpServers"] = True
        with self.assertRaisesRegex(launch.LaunchError, "enableAllProjectMcpServers"):
            launch.validate_claude_settings(tampered)

        tampered = copy.deepcopy(base)
        tampered["disableAllHooks"] = False
        with self.assertRaisesRegex(launch.LaunchError, "disableAllHooks"):
            launch.validate_claude_settings(tampered)

        for bad_allow in (
            "Bash(*)",
            "Bash(sudo:*)",
            "Bash(sailwright provision local:*)",
            "Bash(sailwright install:*)",
            "Bash(git push -f)",
            "Bash(claude --dangerously-skip-permissions:*)",
        ):
            tampered = copy.deepcopy(base)
            tampered["permissions"]["allow"].append(bad_allow)
            with self.subTest(bad_allow=bad_allow), self.assertRaises(launch.LaunchError):
                launch.validate_claude_settings(tampered)

        for removed_substring in (
            "provision local",
            "install",
            "sudo",
            "-f ",
            "--force*",
            "--yolo",
            "--dangerously-skip-permissions",
            "tahoe-runner",
            "~/.ssh/**",
            "find-generic-password",
        ):
            tampered = copy.deepcopy(base)
            tampered["permissions"]["deny"] = [
                rule for rule in tampered["permissions"]["deny"] if removed_substring not in rule
            ]
            with self.subTest(removed=removed_substring), self.assertRaisesRegex(
                launch.LaunchError, "missing required guardrail rules"
            ):
                launch.validate_claude_settings(tampered)

    def test_default_codex_templates_pass_and_enforce_guardrails(self):
        config = tomllib.loads(launch.DEFAULT_CODEX_CONFIG.read_text(encoding="utf-8"))
        validated_cfg = launch.validate_codex_config(config)
        self.assertEqual(validated_cfg["approval_policy"], "on-request")
        self.assertEqual(validated_cfg["sandbox_mode"], "workspace-write")
        self.assertEqual(validated_cfg["project_doc_max_bytes"], 0)
        self.assertEqual(validated_cfg["mcp_servers"], {})

        rules_text = launch.DEFAULT_CODEX_RULES.read_text(encoding="utf-8")
        rules = launch.validate_codex_rules(rules_text)
        forbidden = {rule["pattern"] for rule in rules if rule["decision"] == "forbidden"}
        for expected in launch.REQUIRED_CODEX_FORBIDDEN_PATTERNS:
            self.assertIn(expected, forbidden)

    def test_codex_templates_reject_bypass_and_tampering(self):
        base_cfg = tomllib.loads(launch.DEFAULT_CODEX_CONFIG.read_text(encoding="utf-8"))

        for bad_cfg, pattern in [
            ({**base_cfg, "approval_policy": "never"}, "approval_policy"),
            ({**base_cfg, "approval_policy": "on-failure"}, "approval_policy"),
            ({**base_cfg, "sandbox_mode": "danger-full-access"}, "sandbox_mode"),
            ({**base_cfg, "project_doc_max_bytes": 1024}, "project_doc_max_bytes"),
            ({**base_cfg, "mcp_servers": {"evil": {"command": "sh"}}}, "mcp_servers"),
            ({**base_cfg, "profiles": {"fast": {"approval_policy": "never"}}}, 'must not be "never"'),
            ({**base_cfg, "profiles": {"fast": {"sandbox_mode": "danger-full-access"}}}, "danger-full-access"),
        ]:
            with self.subTest(bad_cfg=bad_cfg), self.assertRaisesRegex(launch.LaunchError, pattern):
                launch.validate_codex_config(bad_cfg)

        base_rules = launch.DEFAULT_CODEX_RULES.read_text(encoding="utf-8")
        with self.assertRaisesRegex(launch.LaunchError, "prefix_rule"):
            launch.validate_codex_rules(base_rules + "\nimport os\n")

        with self.assertRaisesRegex(launch.LaunchError, "must not allow"):
            launch.validate_codex_rules(
                base_rules + '\nprefix_rule(pattern=["sudo"], decision="allow", justification="bad")\n'
            )

        reduced = base_rules.replace('pattern = ["sailwright", "install"]', 'pattern = ["other", "cmd"]')
        with self.assertRaisesRegex(launch.LaunchError, "missing required forbidden rules"):
            launch.validate_codex_rules(reduced)

    def test_scoped_host_change_approval_moves_only_matching_rules_to_prompt(self):
        base_claude = json.loads(launch.DEFAULT_CLAUDE_SETTINGS.read_text(encoding="utf-8"))
        categories = launch.approved_host_change_categories(["sailwright install"])
        self.assertEqual(categories, {"install"})
        effective_claude = launch.materialize_claude_settings(base_claude, categories)
        self.assertIn("Bash(*sailwright* install*)", effective_claude["permissions"]["ask"])
        self.assertNotIn("Bash(*sailwright* install*)", effective_claude["permissions"]["allow"])
        self.assertNotIn("Bash(*sailwright* install*)", effective_claude["permissions"]["deny"])
        # Unapproved host-changing commands and bypass flags stay strictly denied.
        self.assertIn("Bash(*sailwright* provision local*)", effective_claude["permissions"]["deny"])
        self.assertIn("Bash(sudo:*)", effective_claude["permissions"]["deny"])
        self.assertIn("Bash(*--dangerously-skip-permissions*)", effective_claude["permissions"]["deny"])

        base_rules = launch.validate_codex_rules(launch.DEFAULT_CODEX_RULES.read_text(encoding="utf-8"))
        rendered = launch.render_codex_rules(base_rules, categories)
        self.assertIn('pattern = ["sailwright", "install"],\n    decision = "prompt",', rendered)
        self.assertIn('pattern = ["sailwright", "provision", "local"],\n    decision = "forbidden",', rendered)
        self.assertIn('pattern = ["codex", "--yolo"],\n    decision = "forbidden",', rendered)

    def test_skill_and_docs_document_required_guardrails_and_os_guidance(self):
        skill_text = launch.DEFAULT_SKILL_FILE.read_text(encoding="utf-8")
        e2e_skill_text = (REPO_ROOT / ".claude" / "skills" / "sailwright-host-e2e" / "SKILL.md").read_text(
            encoding="utf-8"
        )
        doc_text = (REPO_ROOT / "docs" / "host-test-protocol.md").read_text(encoding="utf-8")

        self.assertIn("sailwright-host-e2e", skill_text)
        self.assertIn("sailwright-host-agent", e2e_skill_text)
        self.assertIn("tahoe-runner", skill_text)
        self.assertIn("boot-utm.py --software-emulation", skill_text)
        self.assertIn("screendump", skill_text)
        self.assertIn("hostfwd_add", skill_text)
        self.assertIn("virtiofs", skill_text)
        self.assertIn("DEV_ALCHEMY_LIBVIRT_URI", skill_text)
        self.assertIn("dedicated macOS user", doc_text)
        self.assertIn("launch.py", doc_text)


@unittest.skipIf(os.name == "nt", "fixture uses a POSIX container and shell stubs")
class EndToEndDryRunTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="host-agent-launch-test-")))
        self.work = self.root / "work"
        self.results_repo = self.root / "results.git"
        self.source_repo = self.root / "source.git"
        self.record_file = self.root / "provider-record.json"
        self.program = [sys.executable, "-B", str(SCRIPTS / "protocol.py")]

        self.env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        gitconfig = self.root / "gitconfig"
        gitconfig.write_text("[user]\n\tname = Test Operator\n\temail = operator@example.org\n", encoding="utf-8")

        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        go_stub = self.bin_dir / "go"
        go_stub.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        go_stub.chmod(0o700)
        for provider in ("claude", "codex"):
            stub = self.bin_dir / provider
            stub.write_text(FAKE_PROVIDER_SCRIPT, encoding="utf-8")
            stub.chmod(0o700)

        self.env.update(
            PATH=os.pathsep.join((str(Path(sys.executable).parent), str(self.bin_dir), self.env["PATH"])),
            GIT_CONFIG_GLOBAL=str(gitconfig),
            GIT_CONFIG_NOSYSTEM="1",
            SAILWRIGHT_HOST_AGENT_BOOTSTRAP=str(SCRIPTS),
            SAILWRIGHT_HOST_AGENT_HOOK=str(SCRIPTS / "launch.py"),
            SAILWRIGHT_HOST_AGENT_PROVIDER="claude",
            SAILWRIGHT_HOST_AGENT_WORK_ROOT=str(self.work),
            FAKE_PROVIDER_RECORD=str(self.record_file),
        )

        self.git("init", "-q", "--bare", str(self.results_repo))
        self.git("init", "-q", "--bare", str(self.source_repo))

        upstream = self.root / "upstream"
        self.git("init", "-q", "-b", "main", str(upstream))
        (upstream / "README.md").write_text("fixture source\n", encoding="utf-8")
        # Add untrusted Claude/MCP config files in source checkout to verify startup isolation.
        (upstream / ".mcp.json").write_text('{"mcpServers": {"untrusted": {"command": "false"}}}\n', encoding="utf-8")
        (upstream / ".claude").mkdir()
        (upstream / ".claude" / "settings.json").write_text(
            '{"permissions": {"defaultMode": "bypassPermissions"}}\n', encoding="utf-8"
        )
        self.git("-C", str(upstream), "add", ".")
        self.git("-C", str(upstream), "commit", "-qm", "Fixture commit")
        self.source_sha = self.git("-C", str(upstream), "rev-parse", "HEAD").strip()
        self.git("-C", str(upstream), "push", "-q", str(self.source_repo), "main")

        os_name, arch = protocol.host_facts()
        self.request = json.loads((SCRIPTS / "examples" / "request.json").read_bytes())
        self.request.update(
            id="dryrun-e2e",
            repo=str(self.source_repo),
            commit=self.source_sha,
            target={"os": os_name, "arch": arch},
            required_tools=[],
            minimum_free_gib=1,
            time_limit_seconds=60,
        )
        self.request_file = self.root / "request.json"

    def run_cli(self, *command, status=0, env=None):
        result = subprocess.run(
            command,
            cwd=self.root,
            env=self.env if env is None else env,
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        self.assertEqual(
            result.returncode,
            status,
            f"{command!r}\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}",
        )
        return result

    def git(self, *args):
        return self.run_cli("git", *args).stdout

    def publish(self):
        self.request_file.write_text(json.dumps(self.request, indent=2) + "\n", encoding="utf-8")
        published = self.run_cli(
            *self.program, "request", str(self.request_file), "--results-repo", str(self.results_repo)
        )
        posix, windows = published.stdout.splitlines()
        ref = f"refs/heads/selftest/{self.request['id']}"
        pin = self.git("--git-dir", str(self.results_repo), "rev-parse", ref).strip()
        return posix, windows, ref, pin

    def assert_pushed_valid_result(self, ref, pin, expected_status="passed"):
        req_id = self.request["id"]
        new_sha = self.git("--git-dir", str(self.results_repo), "rev-parse", ref).strip()
        self.assertNotEqual(new_sha, pin)
        # Fast-forward ancestor check.
        self.run_cli("git", "--git-dir", str(self.results_repo), "merge-base", "--is-ancestor", pin, new_sha)
        # request.json must be preserved byte-for-byte.
        initial_req = subprocess.check_output(
            ["git", "--git-dir", str(self.results_repo), "show", f"{pin}:selftest/{req_id}/request.json"]
        )
        final_req = subprocess.check_output(
            ["git", "--git-dir", str(self.results_repo), "show", f"{new_sha}:selftest/{req_id}/request.json"]
        )
        self.assertEqual(final_req, initial_req)

        result_bytes = subprocess.check_output(
            ["git", "--git-dir", str(self.results_repo), "show", f"{new_sha}:selftest/{req_id}/result.json"]
        )
        validated = protocol.validate("result", result_bytes)
        self.assertEqual(validated["id"], req_id)
        self.assertEqual(validated["commit"], self.source_sha)
        self.assertEqual(validated["request_commit"], pin)
        self.assertEqual(validated["status"], expected_status)
        self.assertEqual(validated["cleanup"]["status"], "verified")
        self.assertTrue(len(validated["follow_up"]) >= 1)

        report_text = self.git("--git-dir", str(self.results_repo), "show", f"{new_sha}:selftest/{req_id}/report.md")
        self.assertIn(self.source_sha, report_text)
        cleanup_text = self.git(
            "--git-dir", str(self.results_repo), "show", f"{new_sha}:selftest/{req_id}/logs/cleanup.txt"
        )
        self.assertIn("Before create:", cleanup_text)
        self.assertIn("After destroy:", cleanup_text)
        self.assertFalse((self.work / "active").exists())

    def test_end_to_end_dry_run_claude_via_bash_start(self):
        posix, _, ref, pin = self.publish()
        self.run_cli("bash", "-c", posix, status=0)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        record = json.loads(self.record_file.read_text(encoding="utf-8"))
        self.assertEqual(record["exe"], "claude")
        self.assertEqual(Path(record["cwd"]).resolve(), SCRIPTS.resolve())
        self.assertIn("--permission-mode", record["argv"])
        self.assertEqual(record["argv"][record["argv"].index("--permission-mode") + 1], "default")
        self.assertIn("--strict-mcp-config", record["argv"])
        self.assertEqual(record["argv"][record["argv"].index("--setting-sources") + 1], "user")
        self.assertNotIn("--dangerously-skip-permissions", record["argv"])
        self.assertEqual(record["claude_settings"]["enableAllProjectMcpServers"], False)
        self.assertEqual(record["claude_settings"]["disableAllHooks"], True)

    def test_end_to_end_dry_run_codex_via_bash_start(self):
        self.request["id"] = "dryrun-codex"
        posix, _, ref, pin = self.publish()
        env = {**self.env, "SAILWRIGHT_HOST_AGENT_PROVIDER": "codex"}
        self.run_cli("bash", "-c", posix, status=0, env=env)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        record = json.loads(self.record_file.read_text(encoding="utf-8"))
        self.assertEqual(record["exe"], "codex")
        self.assertEqual(Path(record["cwd"]).resolve(), SCRIPTS.resolve())
        self.assertIn('approval_policy="on-request"', record["argv"])
        self.assertIn('sandbox_mode="workspace-write"', record["argv"])
        self.assertNotIn("--yolo", record["argv"])
        self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", record["argv"])
        self.assertIn('approval_policy = "on-request"', record["codex_cfg"])
        self.assertIn('pattern = ["sailwright", "install"]', record["codex_rules"])

    @unittest.skipUnless(shutil.which("zsh"), "zsh is unavailable")
    def test_end_to_end_dry_run_via_zsh_start(self):
        self.request["id"] = "dryrun-zsh"
        posix, _, ref, pin = self.publish()
        self.run_cli("zsh", "-c", posix, status=0)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

    @unittest.skipUnless(shutil.which("pwsh"), "PowerShell is unavailable")
    def test_end_to_end_dry_run_via_powershell_start(self):
        self.request["id"] = "dryrun-pwsh"
        _, windows, ref, pin = self.publish()
        self.run_cli("pwsh", "-NoProfile", "-NonInteractive", "-Command", f"{windows}; exit $LASTEXITCODE", status=0)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

    def test_host_changing_approval_scopes_prompt_rules_without_bypass(self):
        self.request.update(
            id="dryrun-approved-install",
            operation_class="host-changing",
            human_approval={"approved_by": "Operator", "scope": ["sailwright install"]},
        )
        posix, _, ref, pin = self.publish()
        # Without --approve-host-changes, starter rejects before launch.
        denied = self.run_cli("bash", "-c", posix, status=1)
        self.assertIn("--approve-host-changes", denied.stderr)
        self.assertFalse(self.record_file.exists())

        # With --approve-host-changes, launch succeeds and moves only install to ask.
        self.run_cli("bash", "-c", posix + " --approve-host-changes", status=0)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")
        record = json.loads(self.record_file.read_text(encoding="utf-8"))
        self.assertIn("Bash(*sailwright* install*)", record["claude_settings"]["permissions"]["ask"])
        self.assertIn("Bash(*sailwright* provision local*)", record["claude_settings"]["permissions"]["deny"])

    def test_timeout_enforced_with_explicit_exit_code_124(self):
        self.request.update(id="dryrun-timeout", time_limit_seconds=1)
        posix, _, _, _ = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "sleep"}
        run = self.run_cli("bash", "-c", posix, status=124, env=env)
        self.assertIn("timed out", run.stderr)
        self.assertFalse((self.work / "active").exists())

    def test_published_failed_result_returns_exit_1(self):
        self.request["id"] = "dryrun-reported-fail"
        posix, _, ref, pin = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "report_failed"}
        run = self.run_cli("bash", "-c", posix, status=1, env=env)
        self.assertIn("reported status failed", run.stderr)
        self.assert_pushed_valid_result(ref, pin, expected_status="failed")

    def test_post_run_verification_rejects_broken_unpushed_or_tampered_results(self):
        cases = [
            ("tamper_request", "byte-for-byte unchanged"),
            ("invalid_schema", "failed schema validation"),
            ("bad_commit", "commit does not match"),
            ("missing_report", "missing required report.md"),
            ("uncommitted", "uncommitted changes"),
            ("unpushed", "expected pushed commit"),
            ("outside_file", "modified path outside"),
            ("symlink_log", "symlinks are forbidden"),
            ("private_key_leak", "private key material"),
        ]
        for index, (behavior, expected_err) in enumerate(cases, start=1):
            with self.subTest(behavior=behavior):
                self.request["id"] = f"dryrun-neg-{index}"
                posix, _, _, _ = self.publish()
                env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": behavior}
                run = self.run_cli("bash", "-c", posix, status=1, env=env)
                self.assertIn(expected_err, run.stderr)
                self.assertFalse((self.work / "active").exists())

    def test_rejects_template_or_skill_inside_untrusted_source_checkout(self):
        self.request["id"] = "dryrun-untrusted-override"
        posix, _, ref, pin = self.publish()
        # Run starter in --dry-run first to inspect work dir, then invoke launch.py directly with untrusted override.
        dry = self.run_cli("bash", "-c", posix + " --dry-run", status=0)
        work_dir = Path(dry.stdout.splitlines()[0].removeprefix("Work directory: "))
        source_dir = work_dir / "source"
        results_dir = work_dir / "results"
        request_path = results_dir / "selftest" / self.request["id"] / "request.json"
        untrusted_settings = source_dir / ".claude" / "settings.json"
        env = {**self.env, "SAILWRIGHT_HOST_AGENT_CLAUDE_SETTINGS": str(untrusted_settings)}
        run = self.run_cli(
            sys.executable, "-B", str(SCRIPTS / "launch.py"),
            "--provider", "claude",
            "--skill", "sailwright-host-agent",
            "--request", str(request_path),
            "--source-dir", str(source_dir),
            "--results-dir", str(results_dir),
            "--results-ref", ref,
            "--request-commit", pin,
            status=1,
            env=env,
        )
        self.assertIn("must not reside inside untrusted checkout", run.stderr)

    def test_rejects_approve_flag_for_vm_lifecycle_and_tampered_override_before_provider(self):
        self.request["id"] = "dryrun-direct-checks"
        posix, _, ref, pin = self.publish()
        dry = self.run_cli("bash", "-c", posix + " --dry-run", status=0)
        work_dir = Path(dry.stdout.splitlines()[0].removeprefix("Work directory: "))
        source_dir = work_dir / "source"
        results_dir = work_dir / "results"
        request_path = results_dir / "selftest" / self.request["id"] / "request.json"

        base_cmd = [
            sys.executable, "-B", str(SCRIPTS / "launch.py"),
            "--provider", "claude",
            "--skill", "sailwright-host-agent",
            "--request", str(request_path),
            "--source-dir", str(source_dir),
            "--results-dir", str(results_dir),
            "--results-ref", ref,
            "--request-commit", pin,
        ]
        # --approve-host-changes must be rejected on vm-lifecycle requests.
        bad_flag = self.run_cli(*base_cmd, "--approve-host-changes", status=1)
        self.assertIn("only permitted for host-changing requests", bad_flag.stderr)
        self.assertFalse(self.record_file.exists())

        # Tampered Claude template (bypassPermissions) must be rejected before launching provider.
        bad_claude = self.root / "bad-claude.json"
        tampered = json.loads(launch.DEFAULT_CLAUDE_SETTINGS.read_text(encoding="utf-8"))
        tampered["permissions"]["defaultMode"] = "bypassPermissions"
        bad_claude.write_text(json.dumps(tampered), encoding="utf-8")
        env = {**self.env, "SAILWRIGHT_HOST_AGENT_CLAUDE_SETTINGS": str(bad_claude)}
        bad_tpl = self.run_cli(*base_cmd, status=1, env=env)
        self.assertIn("bypassPermissions and dontAsk are forbidden", bad_tpl.stderr)
        self.assertFalse(self.record_file.exists())

        # Tampered Codex template (danger-full-access) must be rejected before launching provider.
        bad_codex = self.root / "bad-codex.toml"
        bad_codex.write_text(
            'approval_policy = "on-request"\nsandbox_mode = "danger-full-access"\nproject_doc_max_bytes = 0\n',
            encoding="utf-8",
        )
        codex_cmd = [*base_cmd[:4], "codex", *base_cmd[5:]]
        env_codex = {**self.env, "SAILWRIGHT_HOST_AGENT_CODEX_CONFIG": str(bad_codex)}
        bad_codex_run = self.run_cli(*codex_cmd, status=1, env=env_codex)
        self.assertIn("danger-full-access", bad_codex_run.stderr)
        self.assertFalse(self.record_file.exists())


if __name__ == "__main__":
    unittest.main()
