"""Host-free guardrail, template, and end-to-end dry-run tests for launch.py."""

import copy
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest
from unittest.mock import patch

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


def parse_provider_argv(argv):
    add_dirs = []
    positionals = []
    seen_sep = False
    value_flags = {
        "--permission-mode",
        "--settings",
        "--setting-sources",
        "--mcp-config",
        "--append-system-prompt-file",
        "-c",
        "-C",
    }
    index = 0
    while index < len(argv):
        token = argv[index]
        if token == "--":
            seen_sep = True
            positionals.extend(argv[index + 1:])
            break
        if token == "--add-dir":
            index += 1
            while index < len(argv) and not argv[index].startswith("-"):
                add_dirs.append(argv[index])
                index += 1
            continue
        if token in value_flags:
            index += 2
            continue
        if token.startswith("-"):
            index += 1
            continue
        positionals.append(token)
        index += 1
    return add_dirs, positionals, seen_sep


def main():
    argv = sys.argv[1:]
    behavior = os.environ.get("FAKE_PROVIDER_BEHAVIOR", "pass")
    record_file = os.environ.get("FAKE_PROVIDER_RECORD")

    add_dirs, positionals, seen_sep = parse_provider_argv(argv)
    for directory in add_dirs:
        if not Path(directory).is_dir():
            raise SystemExit(f"add-dir path was not found: {directory}")
    if not seen_sep or argv.index("--") != len(argv) - 2:
        raise SystemExit("positional prompt must be separated with '--'")
    if len(positionals) != 1:
        raise SystemExit(f"expected exactly 1 positional prompt, got {len(positionals)}")

    positional_prompt = positionals[0]
    if "\n" in positional_prompt or "\r" in positional_prompt or not positional_prompt.isascii():
        raise SystemExit("positional prompt must be single-line ASCII")
    if any(ch in positional_prompt for ch in '&|<>%^!"\'`$();*?'):
        raise SystemExit(f"positional prompt contains shell/batch metacharacters: {positional_prompt!r}")

    brief_path = Path.cwd() / "launch-brief.md"
    if brief_path.is_symlink() or not brief_path.is_file():
        raise SystemExit(f"missing launch-brief.md in cwd {Path.cwd()}")
    brief_readonly = (brief_path.stat().st_mode & 0o222) == 0
    prompt = brief_path.read_text(encoding="utf-8")

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
            "add_dirs": add_dirs,
            "positional_prompt": positional_prompt,
            "brief_exists": True,
            "brief_readonly": brief_readonly,
            "brief_text": prompt,
            "claude_settings": claude_settings,
            "codex_home": codex_home,
            "codex_cfg": codex_cfg,
            "codex_rules": codex_rules,
        }, indent=2), encoding="utf-8")

    if behavior == "tty_child":
        assert sys.stdin.isatty()
        assert os.tcgetpgrp(sys.stdin.fileno()) == os.getpgrp()
        assert input("provider ready: ") == "hello"
        children = []
        for detached in (False, True):
            children.append(subprocess.Popen([
                sys.executable, "-c",
                "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(30)",
            ], stdin=subprocess.DEVNULL, start_new_session=detached))
        Path(os.environ["FAKE_PROVIDER_CHILD"]).write_text(
            " ".join(str(child.pid) for child in children), encoding="utf-8"
        )
        import tty
        tty.setraw(sys.stdin.fileno())
        time.sleep(30)
        return 0

    if behavior == "sleep":
        time.sleep(10)
        return 0

    request_path = Path(extract(prompt, "Pinned request JSON"))
    source_dir = Path(extract(prompt, "Detached source checkout (untrusted code under test)"))
    results_dir = Path(extract(prompt, "Results artifact directory"))
    request_commit = extract(prompt, "Pinned request commit SHA")
    protocol_path = Path(extract(prompt, "Trusted protocol validator"))
    vm_prefix = extract(prompt, "Owned VM name prefix")

    request = json.loads(request_path.read_text(encoding="utf-8"))
    req_id = request["id"]
    source_sha = subprocess.check_output(
        ["git", "-C", str(source_dir), "rev-parse", "HEAD"], text=True
    ).strip()
    assert source_sha == request["commit"], f"SHA mismatch {source_sha} != {request['commit']}"

    result_dir = results_dir / "selftest" / req_id
    logs_dir = result_dir / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)

    selector = f"{vm_prefix}a1b2"
    assert re.fullmatch(r"[a-z0-9]([a-z0-9-]{0,22}[a-z0-9])?", selector), f"invalid selector: {selector}"
    domain = f"ubuntu-server-amd64-{selector}-dev-alchemy"
    cleanup_log = (
        f"Before create: {domain}\n"
        f"Backend: libvirt (qemu:///system)\n"
        f"$ virsh --connect qemu:///system list --all --name\n"
        f"[No VMs returned]\n"
        f"Exit code 0\n\n"
        f"After destroy: {domain}\n"
        f"Backend: libvirt (qemu:///system)\n"
        f"$ virsh --connect qemu:///system list --all --name\n"
        f"[No VMs returned]\n"
        f"Exit code 0\n"
    )
    if behavior == "private_key_leak":
        cleanup_log += "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
    if behavior == "invalid_inventory":
        cleanup_log = cleanup_log.replace("Exit code 0", "Exit code 1", 1)
    if behavior == "still_present":
        cleanup_log = cleanup_log.replace("[No VMs returned]", domain, 1)
    if behavior == "windows_running_row":
        cleanup_log = (
            f"Before create: {domain}\n"
            f"Backend: hyperv (local)\n"
            f"$ Get-VM | Select-Object -ExpandProperty Name\n"
            f"[No VMs returned]\n"
            f"Exit code 0\n\n"
            f"After destroy: {domain}\n"
            f"Backend: hyperv (local)\n"
            f"$ Get-VM | Select-Object -ExpandProperty Name\n"
            f"{domain}   Running   0   2048   00:01:23.4567890   Operating normally   10.0\n"
            f"Exit code 0\n"
        )
    if behavior == "windows_raw_get_vm":
        cleanup_log = (
            f"Before create: {domain}\n"
            f"Backend: hyperv (local)\n"
            f"$ Get-VM\n"
            f"Name State\n"
            f"---- -----\n"
            f"other Running\n"
            f"Exit code 0\n\n"
            f"After destroy: {domain}\n"
            f"Backend: hyperv (local)\n"
            f"$ Get-VM\n"
            f"Name State\n"
            f"---- -----\n"
            f"other Running\n"
            f"Exit code 0\n"
        )
    if behavior == "mismatched_libvirt_uri":
        cleanup_log = (
            f"Before create: {domain}\n"
            f"Backend: libvirt (qemu:///system)\n"
            f"$ virsh --connect qemu:///system list --all --name\n"
            f"[No VMs returned]\n"
            f"Exit code 0\n\n"
            f"After destroy: {domain}\n"
            f"Backend: libvirt (qemu:///session)\n"
            f"$ virsh --connect qemu:///session list --all --name\n"
            f"[No VMs returned]\n"
            f"Exit code 0\n"
        )
    if behavior == "command_uri_mismatch":
        cleanup_log = (
            f"Before create: {domain}\n"
            f"Backend: libvirt (qemu:///system)\n"
            f"$ virsh --connect qemu:///system list --all --name\n"
            f"[No VMs returned]\n"
            f"Exit code 0\n\n"
            f"After destroy: {domain}\n"
            f"Backend: libvirt (qemu:///system)\n"
            f"$ virsh --connect qemu:///session list --all --name\n"
            f"[No VMs returned]\n"
            f"Exit code 0\n"
        )
    if behavior == "no_vms":
        cleanup_log = cleanup_log.replace(domain, "no-vms")
    if behavior != "missing_cleanup":
        (logs_dir / "cleanup.txt").write_text(cleanup_log, encoding="utf-8")
    if behavior == "pgp_private_key_leak":
        (logs_dir / "pgp.txt").write_text(
            "-----BEGIN PGP PRIVATE KEY BLOCK-----\nfake-synthetic-body\n-----END PGP PRIVATE KEY BLOCK-----\n",
            encoding="utf-8",
        )
    if behavior == "utf16_openssh_key_leak":
        (logs_dir / "win-step.txt").write_bytes(
            "-----BEGIN OPENSSH PRIVATE KEY-----\r\nfake-synthetic-body\r\n-----END OPENSSH PRIVATE KEY-----\r\n"
            .encode("utf-16")
        )
    if behavior == "opaque_binary_log":
        (logs_dir / "opaque.bin").write_bytes(b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00")
    if behavior == "bomless_utf16_log":
        (logs_dir / "bomless.txt").write_bytes("-----BEGIN OPENSSH PRIVATE KEY-----".encode("utf-16-le"))
    if behavior == "utf16_valid_log":
        (logs_dir / "win-redacted.txt").write_bytes("Guest password: [REDACTED]\r\n".encode("utf-16"))
    (logs_dir / "step.txt").write_bytes(b"Guest password: [REDACTED]\r\n")

    binary_sha = hashlib.sha256(f"fake-sailwright-{source_sha}".encode("utf-8")).hexdigest()
    overall_status = "failed" if behavior in {"report_failed", "fail_then_idle", "incomplete_cleanup"} else "passed"
    step_status = "failed" if behavior in {"report_failed", "fail_then_idle", "incomplete_cleanup"} else "passed"
    step_exit = 2 if behavior in {"report_failed", "fail_then_idle", "incomplete_cleanup"} else 0

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
                "command": f"DEV_ALCHEMY_LIBVIRT_URI=qemu:///system ./sailwright create ubuntu --name {selector}",
                "duration_seconds": 0.4,
            },
        ],
        "cleanup": {
            "status": "verified",
            "owned_vms": [] if behavior == "no_vms" else [domain],
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

    if behavior == "incomplete_cleanup":
        result_doc["cleanup"]["status"] = "incomplete"
        result_doc["cleanup"]["remaining_vms"] = [domain]
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

    if behavior == "provider_git_history":
        subprocess.run(["git", "init", "--quiet", str(results_dir)], check=True)

    if behavior == "symlink_log":
        (logs_dir / "link.txt").symlink_to(request_path)

    if behavior not in {"invalid_schema", "bad_commit", "tamper_request", "missing_report", "private_key_leak", "symlink_log"}:
        subprocess.run(
            [sys.executable, "-B", str(protocol_path), "validate", "result", str(result_file)],
            check=True,
        )

    if behavior in {"extra_ref", "origin_redirect"}:
        assert not (results_dir / ".git").exists()
        assert "SAILWRIGHT_HOST_AGENT_RESULTS_REPO" not in os.environ
        bootstrap_repo = results_dir.parent / "results"
        if behavior == "origin_redirect":
            target = os.environ["FAKE_REDIRECT_REPO"]
            subprocess.run(["git", "-C", str(bootstrap_repo), "remote", "add", "origin", target], check=True)
            remote = "origin"
        else:
            remote = os.environ["FAKE_ORIGINAL_REPO"]
        blocked = subprocess.run([
            "git", "-C", str(bootstrap_repo), "push", remote, "HEAD:refs/heads/unassigned-probe",
        ], capture_output=True, text=True)
        assert blocked.returncode != 0 and "transport 'file' not allowed" in blocked.stderr, blocked
    if behavior in {"pass_then_idle", "fail_then_idle"}:
        time.sleep(10)
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
        self.assertEqual(validated["permissions"]["defaultMode"], "auto")
        self.assertIn(validated["permissions"]["defaultMode"], launch.ALLOWED_CLAUDE_MODES)
        self.assertNotIn("bypassPermissions", validated["permissions"]["defaultMode"])
        self.assertNotIn("dontAsk", validated["permissions"]["defaultMode"])

        # Routine edits and guest VM provisioning are not stalled by blanket ask rules.
        ask = validated["permissions"]["ask"]
        self.assertNotIn("Edit", ask)
        self.assertNotIn("Write", ask)
        self.assertNotIn("Bash(./sailwright provision:*)", ask)
        self.assertNotIn("Bash(sailwright provision:*)", ask)
        self.assertIn("Bash(tart clone:*)", ask)
        self.assertIn("Bash(tart run:*)", ask)
        self.assertIn("Bash(utmctl:*)", ask)

        # Static allow must use explicit virsh URIs and no wildcard git -C rules.
        allow = validated["permissions"]["allow"]
        self.assertIn("Bash(virsh --connect qemu:///system list *)", allow)
        self.assertIn("Bash(virsh --connect qemu:///session list *)", allow)
        self.assertFalse(any(rule.startswith("Bash(git -C *") for rule in allow))

        # No rule may mix '*' with trailing ':*'.
        for section in ("allow", "ask", "deny"):
            for rule in validated["permissions"][section]:
                self.assertFalse(launch.has_mixed_bash_glob_prefix(rule), rule)

        # Confirm default fallback mode is also accepted when all guardrails stay intact.
        fallback_variant = copy.deepcopy(settings)
        fallback_variant["permissions"]["defaultMode"] = "default"
        launch.validate_claude_settings(fallback_variant)

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

        # Mixed '*' + trailing ':*' rules must be rejected across allow, ask, and deny.
        for section, bad_rule in (
            ("allow", "Bash(virsh --connect * list:*)"),
            ("ask", "Bash(git -C * push:*)"),
            ("deny", "Bash(virsh * undefine:*)"),
        ):
            tampered = copy.deepcopy(base)
            tampered["permissions"][section].append(bad_rule)
            with self.subTest(section=section, bad_rule=bad_rule), self.assertRaisesRegex(
                launch.LaunchError, "mixes '\\*' with trailing ':\\*'"
            ):
                launch.validate_claude_settings(tampered)

        for bad_allow in (
            "Bash(*)",
            "Bash(sudo:*)",
            "Bash(sailwright provision local:*)",
            "Bash(sailwright install:*)",
            "Bash(sailwright self-update:*)",
            "Bash(git push -f)",
            "Bash(git -C * status *)",
            "Bash(claude --dangerously-skip-permissions:*)",
        ):
            tampered = copy.deepcopy(base)
            tampered["permissions"]["allow"].append(bad_allow)
            with self.subTest(bad_allow=bad_allow), self.assertRaises(launch.LaunchError):
                launch.validate_claude_settings(tampered)

        for removed_substring in (
            "provision local",
            "install",
            "self-update",
            "sudo",
            "Bash(git:*)",
            "Bash(claude:*)",
            "Bash(codex:*)",
            "tahoe-runner",
            "virsh * undefine",
            "virsh * destroy",
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

    def test_materialize_claude_settings_protects_trusted_dirs(self):
        base = json.loads(launch.DEFAULT_CLAUDE_SETTINGS.read_text(encoding="utf-8"))
        protected_dirs = (SCRIPTS, launch.DEFAULT_SKILL_FILE.parent)
        effective = launch.materialize_claude_settings(base, set(), protected_dirs=protected_dirs)
        deny = effective["permissions"]["deny"]
        self.assertTrue(set(launch.CLAUDE_HOOK_ONLY_RULES).issubset(deny))
        for protected in protected_dirs:
            glob = launch.claude_abs_dir_glob(protected)
            self.assertIn(f"Edit({glob})", deny)
            self.assertIn(f"Write({glob})", deny)
        self.assertEqual(
            launch.claude_abs_dir_glob(r"C:\trusted\sailwright\scripts\host-agent"),
            "//C:/trusted/sailwright/scripts/host-agent/**",
        )

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
            ({**base_cfg, "sandbox_workspace_write": {"network_access": True}}, "network_access"),
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

        for executable in ("git", "codex", "claude"):
            with self.subTest(removed=executable), self.assertRaisesRegex(
                launch.LaunchError, "missing required forbidden rules"
            ):
                launch.validate_codex_rules(base_rules.replace(f'"{executable}", ', "", 1))

        for original in (
            'pattern = ["sailwright", "install"]',
            'pattern = ["sailwright", "self-update"]',
            'pattern = ["./sailwright", "self-update"]',
            'pattern = ["virsh", "--connect", "qemu:///system", "undefine"]',
            'pattern = ["virsh", "-c", "qemu:///session", "destroy"]',
        ):
            reduced = base_rules.replace(original, 'pattern = ["other", "cmd"]')
            with self.subTest(removed=original), self.assertRaisesRegex(
                launch.LaunchError, "missing required forbidden rules"
            ):
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
        self.assertIn("Bash(*sailwright* self-update*)", effective_claude["permissions"]["deny"])
        self.assertIn("Bash(sudo:*)", effective_claude["permissions"]["deny"])
        self.assertIn("Bash(*--dangerously-skip-permissions*)", effective_claude["permissions"]["deny"])

        self_update_categories = launch.approved_host_change_categories(["sailwright self-update"])
        self.assertEqual(self_update_categories, {"self_update"})
        effective_claude_su = launch.materialize_claude_settings(base_claude, self_update_categories)
        self.assertIn("Bash(*sailwright* self-update*)", effective_claude_su["permissions"]["ask"])
        self.assertIn("Bash(*sailwright* install*)", effective_claude_su["permissions"]["deny"])
        self.assertIn("Bash(*sailwright* provision local*)", effective_claude_su["permissions"]["deny"])

        base_rules = launch.validate_codex_rules(launch.DEFAULT_CODEX_RULES.read_text(encoding="utf-8"))
        with tempfile.TemporaryDirectory(prefix="codex-rules-test-") as tmp:
            root = Path(tmp).resolve()
            source_dir = root / "source"
            results_dir = root / "results"
            source_dir.mkdir()
            results_dir.mkdir()

            rendered_default = launch.render_codex_rules(
                base_rules, set(), source_dir=source_dir
            )
            parsed_default = {
                rule["pattern"]: rule["decision"] for rule in launch.validate_codex_rules(rendered_default)
            }
            self.assertEqual(parsed_default[("git",)], "forbidden")
            for bin_path in (str(source_dir / "sailwright"), str(source_dir / "sailwright.exe")):
                self.assertEqual(parsed_default[(bin_path, "provision", "local")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "install")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "self-update")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "destroy", "all")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "stop", "all")], "forbidden")

            rendered_approved = launch.render_codex_rules(
                base_rules, categories, source_dir=source_dir
            )
            parsed_approved = {
                rule["pattern"]: rule["decision"] for rule in launch.parse_codex_rules(rendered_approved)
            }
            self.assertEqual(parsed_approved[("sailwright", "install")], "prompt")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "install")], "prompt")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright.exe"), "install")], "prompt")
            self.assertEqual(parsed_approved[("sailwright", "provision", "local")], "forbidden")
            self.assertEqual(parsed_approved[("sailwright", "self-update")], "forbidden")
            self.assertEqual(parsed_approved[("./sailwright", "self-update")], "forbidden")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "provision", "local")], "forbidden")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "self-update")], "forbidden")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "destroy", "all")], "forbidden")
            self.assertEqual(parsed_approved[("git",)], "forbidden")
            self.assertEqual(parsed_approved[("codex",)], "forbidden")

            rendered_su = launch.render_codex_rules(
                base_rules, self_update_categories, source_dir=source_dir
            )
            parsed_su = {
                rule["pattern"]: rule["decision"] for rule in launch.parse_codex_rules(rendered_su)
            }
            self.assertEqual(parsed_su[("sailwright", "self-update")], "prompt")
            self.assertEqual(parsed_su[("./sailwright", "self-update")], "prompt")
            self.assertEqual(parsed_su[(str(source_dir / "sailwright"), "self-update")], "prompt")
            self.assertEqual(parsed_su[("sailwright", "install")], "forbidden")

    @unittest.skipUnless(shutil.which("codex"), "offline Codex policy engine not installed")
    def test_codex_execpolicy_forbids_git_and_nested_launches(self):
        base = launch.validate_codex_rules(launch.DEFAULT_CODEX_RULES.read_text(encoding="utf-8"))
        with tempfile.TemporaryDirectory(prefix="codex-policy-") as tmp:
            rules = Path(tmp) / "runtime.rules"
            rules.write_text(launch.render_codex_rules(base, {"install"}, Path(tmp)), encoding="utf-8")
            git_path = shutil.which("git")
            for expected, argv in (
                ("forbidden", ["git", "-C", tmp, "push", "origin", "HEAD:refs/heads/selftest/x", "-f"]),
                ("forbidden", ["git", "-c", "color.ui=false", "push", "origin", "--force"]),
                ("forbidden", [git_path, "push", "origin", "HEAD:refs/heads/selftest/x", "--force-with-lease"]),
                ("forbidden", ["codex", "exec", "--yolo"]),
                ("forbidden", ["./codex", "exec", "--yolo"]),
                ("forbidden", ["codex", "-C", tmp, "--dangerously-bypass-approvals-and-sandbox"]),
                ("forbidden", ["claude.cmd", "--model", "test", "--dangerously-skip-permissions"]),
                ("prompt", ["sailwright", "install"]),
                ("forbidden", ["sailwright", "provision", "local"]),
                ("forbidden", ["sailwright", "self-update"]),
                ("forbidden", ["./sailwright", "self-update"]),
            ):
                with self.subTest(argv=argv):
                    result = subprocess.check_output(
                        ["codex", "execpolicy", "check", "--rules", str(rules), "--", *argv],
                        text=True,
                    )
                    self.assertEqual(json.loads(result)["decision"], expected)

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
        self.assertIn("/exit", skill_text)
        self.assertIn("Ctrl+C", skill_text)
        self.assertIn("dedicated macOS user", doc_text)
        self.assertIn("launch.py", doc_text)
        self.assertIn("launch-brief.md", doc_text)
        self.assertIn("/exit", doc_text)
        self.assertIn("Ctrl+C", doc_text)

    def test_owned_vm_prefix_boundary_and_max_length(self):
        selector_pattern = re.compile(r"^[a-z0-9]([a-z0-9-]{0,22}[a-z0-9])?$")
        # Short IDs up to 16 chars preserve exact sw-<id>- compatibility.
        self.assertEqual(launch.owned_vm_prefix("linux-lifecycle"), "sw-linux-lifecycle-")
        id_16 = "a" * 16
        prefix_16 = launch.owned_vm_prefix(id_16)
        self.assertEqual(prefix_16, f"sw-{id_16}-")
        self.assertEqual(len(prefix_16 + "a1b2"), 24)
        self.assertRegex(prefix_16 + "a1b2", selector_pattern)

        # Past the 16-char boundary (17, 27, 48 chars), prefix stays bounded <= 20 chars
        # so a 4-char suffix always fits Sailwright's 24-character maximum.
        for req_id in ("a" * 17, "a" * 27, "long-request-" + "a" * 35, "a----" + "b" * 43):
            prefix = launch.owned_vm_prefix(req_id)
            selector = f"{prefix}a1b2"
            with self.subTest(req_id_len=len(req_id)):
                self.assertLessEqual(len(prefix), launch.MAX_OWNED_VM_PREFIX_LEN)
                self.assertLessEqual(len(selector), launch.MAX_SAILWRIGHT_SELECTOR_LEN)
                self.assertTrue(prefix.startswith("sw-") and prefix.endswith("-"))
                self.assertRegex(selector, selector_pattern)

        # Collision-resistant across full 48-char IDs differing only in the last char.
        max_a = "long-request-" + "a" * 34 + "1"
        max_b = "long-request-" + "a" * 34 + "2"
        self.assertEqual(len(max_a), 48)
        self.assertEqual(len(max_b), 48)
        self.assertNotEqual(launch.owned_vm_prefix(max_a), launch.owned_vm_prefix(max_b))

    def test_posix_two_snapshot_reparented_child_cannot_confirm_termination(self):
        class FakeProcess:
            pid = 501

            def poll(self):
                return 0

            def wait(self, timeout=None):
                return 0

        class FakeScanner:
            def __init__(self, output):
                self.pid = 9999
                self.returncode = 0
                self._output = output

            def communicate(self, timeout=None):
                return self._output, ""

        snapshots = iter([
            "501 100 501 S Mon Jan  1 00:00:00 2026\n",
            "502 1 502 S Mon Jan  1 00:00:01 2026\n",
        ])

        with patch.object(launch.subprocess, "Popen", side_effect=lambda *a, **kw: FakeScanner(next(snapshots))):
            tree = launch.PosixProcessTree(FakeProcess(), adopted_parent=None)
            self.assertEqual(list(tree.live_members()), [501])
            # Leader exited and ordinary job-control child 502 was reparented to PID 1.
            self.assertEqual(tree.live_members(), {})

        # stop() on a non-adopting POSIX path must NOT confirm termination even when live_members() is empty.
        stop_snapshots = iter(["502 1 502 S Mon Jan  1 00:00:01 2026\n"])
        with patch.object(launch.subprocess, "Popen", side_effect=lambda *a, **kw: FakeScanner(next(stop_snapshots))):
            tree = launch.PosixProcessTree(FakeProcess(), adopted_parent=None)
            self.assertFalse(tree.stop())

        # With Linux subreaper adoption, a child reparented to adopted_parent stays tracked.
        adopted_snapshots = iter(["502 100 502 S Mon Jan  1 00:00:01 2026\n"])
        with patch.object(launch.subprocess, "Popen", side_effect=lambda *a, **kw: FakeScanner(next(adopted_snapshots))):
            tree = launch.PosixProcessTree(FakeProcess(), adopted_parent=100)
            self.assertEqual(list(tree.live_members()), [502])

    def test_verify_cleanup_canonical_backends_and_rejections(self):
        vm = "ubuntu-server-amd64-sw-demo-a1b2-dev-alchemy"
        result_doc = {"cleanup": {"status": "verified", "owned_vms": [vm], "remaining_vms": []}}

        valid_cases = [
            ("libvirt", "qemu:///system", "$ virsh --connect qemu:///system list --all --name", "[No VMs returned]"),
            ("libvirt", "qemu:///session", "$ virsh --connect qemu:///session list --all --name", "foreign-domain"),
            ("hyperv", "local", "$ Get-VM | Select-Object -ExpandProperty Name", "Windows 11"),
            ("tart", "local", "$ tart list -q", "tahoe-runner"),
            (
                "utm",
                "local",
                "$ utmctl list | awk 'NR > 1 { sub(/^[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+/, \"\"); print }'",
                "Ubuntu Dev",
            ),
        ]
        for backend, conn, cmd, output in valid_cases:
            log = (
                f"Before create: {vm}\nBackend: {backend} ({conn})\n{cmd}\n{output}\nExit code 0\n\n"
                f"After destroy: {vm}\nBackend: {backend} ({conn})\n{cmd}\n{output}\nExit code 0\n"
            )
            with self.subTest(backend=backend, conn=conn):
                launch.verify_cleanup(result_doc, log.encode("utf-8"))

        # Legacy format without Backend header is rejected.
        legacy = (
            f"Before create: {vm}\n$ virsh --connect qemu:///system list --all --name\n[No VMs returned]\nExit code 0\n\n"
            f"After destroy: {vm}\n$ virsh --connect qemu:///system list --all --name\n[No VMs returned]\nExit code 0\n"
        )
        with self.assertRaisesRegex(launch.LaunchError, "canonical `Backend:"):
            launch.verify_cleanup(result_doc, legacy.encode("utf-8"))

        # Single-space collapsed Hyper-V Running row is rejected.
        collapsed_row = (
            f"Before create: {vm}\nBackend: hyperv (local)\n$ Get-VM | Select-Object -ExpandProperty Name\n[No VMs returned]\nExit code 0\n\n"
            f"After destroy: {vm}\nBackend: hyperv (local)\n$ Get-VM | Select-Object -ExpandProperty Name\n{vm} Running\nExit code 0\n"
        )
        with self.assertRaisesRegex(launch.LaunchError, "still lists an owned VM"):
            launch.verify_cleanup(result_doc, collapsed_row.encode("utf-8"))

        # Mixed empty sentinel and VM name is rejected.
        mixed = (
            f"Before create: {vm}\nBackend: tart (local)\n$ tart list -q\n[No VMs returned]\ntahoe-runner\nExit code 0\n\n"
            f"After destroy: {vm}\nBackend: tart (local)\n$ tart list -q\n[No VMs returned]\nExit code 0\n"
        )
        with self.assertRaisesRegex(launch.LaunchError, "cannot mix"):
            launch.verify_cleanup(result_doc, mixed.encode("utf-8"))

        # Raw table command for Tart without -q/--quiet is rejected.
        raw_tart = (
            f"Before create: {vm}\nBackend: tart (local)\n$ tart list\n[No VMs returned]\nExit code 0\n\n"
            f"After destroy: {vm}\nBackend: tart (local)\n$ tart list\n[No VMs returned]\nExit code 0\n"
        )
        with self.assertRaisesRegex(launch.LaunchError, "tart list -q"):
            launch.verify_cleanup(result_doc, raw_tart.encode("utf-8"))


@unittest.skipUnless(os.name == "nt", "requires native Windows Job Objects")
class WindowsProcessTreeTests(unittest.TestCase):
    def test_provider_timeout_terminates_the_windows_job(self):
        import ctypes
        from ctypes import wintypes

        with tempfile.TemporaryDirectory(prefix="host-agent-job-") as tmp:
            root = Path(tmp)
            child_record = root / "child-pid"
            provider = """
from pathlib import Path
import subprocess
import sys
import time
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"],
                         creationflags=subprocess.CREATE_NEW_PROCESS_GROUP)
Path(sys.argv[1]).write_text(str(child.pid), encoding="ascii")
time.sleep(60)
"""
            status = launch.run_windows_provider(
                [sys.executable, "-B", "-c", provider, str(child_record)],
                root, dict(os.environ), 3, root,
            )
            self.assertEqual(status, (124, True))
            pid = int(child_record.read_text(encoding="ascii"))
            kernel = ctypes.WinDLL("kernel32", use_last_error=True)
            kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
            kernel.OpenProcess.restype = wintypes.HANDLE
            handle = kernel.OpenProcess(0x00100000, False, pid)  # SYNCHRONIZE
            if not handle:
                self.assertEqual(ctypes.get_last_error(), 87)  # ERROR_INVALID_PARAMETER
                return
            kernel.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
            kernel.WaitForSingleObject.restype = wintypes.DWORD
            kernel.CloseHandle.argtypes = [wintypes.HANDLE]
            try:
                self.assertEqual(kernel.WaitForSingleObject(handle, 0), 0)  # WAIT_OBJECT_0
            finally:
                kernel.CloseHandle(handle)


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
        self.assertEqual(self.git("--git-dir", str(self.results_repo), "rev-list", "--parents",
                                  f"{pin}..{new_sha}").splitlines(), [f"{new_sha} {pin}"])
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
        self.assertEqual(subprocess.check_output([
            "git", "--git-dir", str(self.results_repo), "show", f"{new_sha}:selftest/{req_id}/logs/step.txt",
        ]), b"Guest password: [REDACTED]\r\n")
        self.assertFalse((self.work / "active").exists())

    def test_end_to_end_dry_run_claude_via_bash_start(self):
        posix, _, ref, pin = self.publish()
        self.run_cli("bash", "-c", posix, status=0)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        record = json.loads(self.record_file.read_text(encoding="utf-8"))
        self.assertEqual(record["exe"], "claude")
        self.assertNotEqual(Path(record["cwd"]).resolve(), SCRIPTS.resolve())
        self.assertEqual(Path(record["cwd"]).name, "workspace")
        self.assertFalse(Path(record["cwd"]).exists())
        self.assertTrue(record["brief_exists"])
        self.assertTrue(record["brief_readonly"])
        self.assertEqual(record["argv"][-2], "--")
        self.assertEqual(record["argv"][-1], launch.LAUNCH_PROMPT)
        self.assertEqual(len(record["add_dirs"]), 2)
        self.assertIn("--permission-mode", record["argv"])
        self.assertEqual(record["argv"][record["argv"].index("--permission-mode") + 1], "auto")
        self.assertIn("--strict-mcp-config", record["argv"])
        self.assertEqual(record["argv"][record["argv"].index("--setting-sources") + 1], "user")
        self.assertNotIn("--dangerously-skip-permissions", record["argv"])
        self.assertEqual(record["claude_settings"]["enableAllProjectMcpServers"], False)
        self.assertEqual(record["claude_settings"]["disableAllHooks"], True)
        _, artifacts_dir = record["add_dirs"]
        self.assertEqual(Path(artifacts_dir).name, "artifacts")
        self.assertFalse((Path(artifacts_dir) / ".git").exists())
        self.assertIn("Bash(git:*)", record["claude_settings"]["permissions"]["deny"])
        self.assertIn(
            f"Edit({launch.claude_abs_dir_glob(SCRIPTS)})",
            record["claude_settings"]["permissions"]["deny"],
        )
        self.assertIn(
            f"Write({launch.claude_abs_dir_glob(launch.DEFAULT_SKILL_FILE.parent)})",
            record["claude_settings"]["permissions"]["deny"],
        )

    def test_end_to_end_dry_run_codex_via_bash_start(self):
        self.request["id"] = "dryrun-codex"
        posix, _, ref, pin = self.publish()
        env = {**self.env, "SAILWRIGHT_HOST_AGENT_PROVIDER": "codex"}
        self.run_cli("bash", "-c", posix, status=0, env=env)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        record = json.loads(self.record_file.read_text(encoding="utf-8"))
        self.assertEqual(record["exe"], "codex")
        self.assertNotEqual(Path(record["cwd"]).resolve(), SCRIPTS.resolve())
        self.assertEqual(Path(record["cwd"]).name, "workspace")
        self.assertFalse(Path(record["cwd"]).exists())
        self.assertEqual(record["argv"][record["argv"].index("-C") + 1], record["cwd"])
        self.assertEqual(record["argv"][-2], "--")
        self.assertEqual(record["argv"][-1], launch.LAUNCH_PROMPT)
        self.assertTrue(record["brief_exists"])
        self.assertTrue(record["brief_readonly"])
        self.assertIn('approval_policy="on-request"', record["argv"])
        self.assertIn('sandbox_mode="workspace-write"', record["argv"])
        self.assertNotIn("--yolo", record["argv"])
        self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", record["argv"])
        self.assertIn('approval_policy = "on-request"', record["codex_cfg"])
        self.assertIn('pattern = ["sailwright", "install"]', record["codex_rules"])
        source_dir, _ = record["add_dirs"]
        self.assertIn(json.dumps(["git"]), record["codex_rules"])
        self.assertIn(json.dumps(["codex"]), record["codex_rules"])
        self.assertIn(json.dumps([str(Path(source_dir) / "sailwright"), "install"]), record["codex_rules"])

    def test_canonical_paths_resolved_through_symlinked_work_root(self):
        self.request["id"] = "dryrun-symlink-root"
        posix, _, ref, pin = self.publish()
        self.work.mkdir(exist_ok=True)
        work_link = self.root / "work-symlink"
        work_link.symlink_to(self.work)
        env = {**self.env, "SAILWRIGHT_HOST_AGENT_WORK_ROOT": str(work_link)}
        self.run_cli("bash", "-c", posix, status=0, env=env)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        record = json.loads(self.record_file.read_text(encoding="utf-8"))
        canonical_work = str(self.work.resolve())
        for directory in record["add_dirs"]:
            self.assertTrue(directory.startswith(canonical_work), f"{directory} not under {canonical_work}")
            self.assertNotIn("work-symlink", directory)
        self.assertNotIn("work-symlink", record["brief_text"])
        self.assertEqual(Path(record["add_dirs"][1]).name, "artifacts")

    def test_fake_provider_rejects_unseparated_variadic_add_dir_prompt(self):
        # Proves the test stub fails if '--' is omitted after variadic --add-dir.
        bad = self.run_cli(
            str(self.bin_dir / "claude"),
            "--add-dir", str(self.root),
            "unseparated prompt text",
            status=1,
        )
        self.assertIn("was not found", bad.stderr)

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

    def test_timeout_without_result_retains_lock_until_recovery(self):
        self.request.update(id="dryrun-timeout", time_limit_seconds=1)
        posix, _, ref, pin = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "sleep"}
        run = self.run_cli("bash", "-c", posix, status=124, env=env)
        self.assertIn("timed out", run.stderr)
        self.assertTrue((self.work / "active" / "cleanup-pending").is_file())
        self.assert_unpublished(ref, pin)

        self.request["id"] = "dryrun-second"
        next_request, _, next_ref, next_pin = self.publish()
        excluded = self.run_cli("bash", "-c", next_request, status=1)
        self.assertIn("an active request lock exists", excluded.stderr)
        shutil.rmtree(self.work / "active")
        self.run_cli("bash", "-c", next_request)
        self.assert_pushed_valid_result(next_ref, next_pin)

    def test_tty_timeout_stops_provider_child_and_restores_operator_terminal(self):
        import errno
        import pty

        self.request.update(id="dryrun-tty", time_limit_seconds=2)
        posix, _, ref, pin = self.publish()
        child_pid_file = self.root / "provider-child"
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "tty_child", "FAKE_PROVIDER_CHILD": str(child_pid_file)}
        pid, fd = pty.fork()
        if pid == 0:
            os.chdir(self.root)
            os.execvpe("bash", ["bash", "-c", f'{posix}; rc=$?; printf "operator alive: %s\\n" "$rc"; exit "$rc"'], env)
        output = bytearray()
        try:
            import select

            answered = False
            while select.select([fd], [], [], 15)[0]:
                try:
                    data = os.read(fd, 4096)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    break
                if not data:
                    break
                output.extend(data)
                if not answered and b"provider ready:" in output:
                    os.write(fd, b"hello\n")
                    answered = True
            else:
                self.fail(f"PTY session stalled: {output!r}")
        finally:
            os.close(fd)
            _, status = os.waitpid(pid, 0)
        self.assertEqual(os.waitstatus_to_exitcode(status), 124, output)
        self.assertIn(b"operator alive: 124\r\n", output)
        for child_pid in child_pid_file.read_text(encoding="utf-8").split():
            child = subprocess.run(["ps", "-o", "stat=", "-p", child_pid], capture_output=True, text=True)
            self.assertTrue(not child.stdout.strip() or child.stdout.strip().startswith("Z"), child.stdout)
        self.assertTrue((self.work / "active" / "cleanup-pending").is_file())
        self.assert_unpublished(ref, pin)

    def test_idle_provider_timeout_publishes_and_maps_result_status(self):
        self.request.update(id="dryrun-idle-pass", time_limit_seconds=2)
        posix, _, ref, pin = self.publish()
        env_pass = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "pass_then_idle"}
        run_pass = self.run_cli("bash", "-c", posix, status=0, env=env_pass)
        self.assertIn("(status: passed)", run_pass.stderr)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        self.request.update(id="dryrun-idle-fail", time_limit_seconds=2)
        posix_fail, _, ref_fail, pin_fail = self.publish()
        env_fail = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "fail_then_idle"}
        run_fail = self.run_cli("bash", "-c", posix_fail, status=1, env=env_fail)
        self.assertIn("(status: failed)", run_fail.stderr)
        self.assert_pushed_valid_result(ref_fail, pin_fail, expected_status="failed")

    def test_published_failed_result_returns_exit_1(self):
        self.request["id"] = "dryrun-reported-fail"
        posix, _, ref, pin = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "report_failed"}
        run = self.run_cli("bash", "-c", posix, status=1, env=env)
        self.assertIn("(status: failed)", run.stderr)
        self.assert_pushed_valid_result(ref, pin, expected_status="failed")

    def assert_unpublished(self, ref, pin, repo=None):
        repo = repo or self.results_repo
        self.assertEqual(self.git("--git-dir", str(repo), "for-each-ref", "--format=%(refname) %(objectname)").strip(),
                         f"{ref} {pin}")
        self.assertEqual(
            self.git("--git-dir", str(repo), "ls-tree", "-r", "--name-only", ref).splitlines(),
            [f"selftest/{self.request['id']}/request.json"],
        )

    def test_invalid_or_incomplete_results_never_reach_remote(self):
        cases = [
            ("tamper_request", "byte-for-byte unchanged"),
            ("invalid_schema", "failed schema validation"),
            ("bad_commit", "commit does not match"),
            ("missing_report", "missing required report.md"),
            ("outside_file", "unexpected path in results"),
            ("symlink_log", "symlinks and reparse points are forbidden"),
            ("private_key_leak", "private key material"),
            ("pgp_private_key_leak", "private key material"),
            ("utf16_openssh_key_leak", "private key material"),
            ("opaque_binary_log", "unsupported opaque or binary bytes"),
            ("bomless_utf16_log", "unsupported opaque or binary bytes"),
            ("missing_cleanup", "missing required nonempty logs/cleanup.txt"),
            ("invalid_inventory", "exit code 0 per phase"),
            ("still_present", "inventory still lists an owned VM"),
            ("windows_running_row", "inventory still lists an owned VM"),
            ("windows_raw_get_vm", "Hyper-V inventory command"),
            ("mismatched_libvirt_uri", "same backend and connection"),
            ("command_uri_mismatch", "libvirt inventory command must target only qemu:///system"),
            ("provider_git_history", "invalid result path '.git'"),
            ("incomplete_cleanup", "reports incomplete cleanup"),
        ]
        for index, (behavior, expected_err) in enumerate(cases, start=1):
            with self.subTest(behavior=behavior):
                self.request["id"] = f"dryrun-neg-{index}"
                posix, _, ref, pin = self.publish()
                env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": behavior}
                objects_before = self.git("--git-dir", str(self.results_repo), "count-objects", "-v")
                run = self.run_cli("bash", "-c", posix, status=1, env=env)
                self.assertIn(expected_err, run.stderr)
                self.assertEqual(self.git("--git-dir", str(self.results_repo), "rev-parse", ref).strip(), pin)
                self.assertEqual(self.git("--git-dir", str(self.results_repo), "count-objects", "-v"), objects_before)
                self.assertTrue((self.work / "active" / "cleanup-pending").is_file())
                self.assertIn("native hypervisor inventory", run.stderr)
                if behavior == "incomplete_cleanup":
                    excluded = self.run_cli("bash", "-c", posix, status=1)
                    self.assertIn("an active request lock exists", excluded.stderr)
                # Simulate operator recovery on the VM-free fixture.
                shutil.rmtree(self.work / "active")

    def test_no_vm_run_publishes_with_both_inventories(self):
        posix, _, ref, pin = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "no_vms"}
        self.run_cli("bash", "-c", posix, env=env)
        self.assert_pushed_valid_result(ref, pin)

    def test_valid_utf16_redacted_log_passes_and_preserves_original_bytes(self):
        self.request["id"] = "dryrun-utf16-ok"
        posix, _, ref, pin = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "utf16_valid_log"}
        self.run_cli("bash", "-c", posix, status=0, env=env)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")
        new_sha = self.git("--git-dir", str(self.results_repo), "rev-parse", ref).strip()
        stored = subprocess.check_output([
            "git", "--git-dir", str(self.results_repo), "show",
            f"{new_sha}:selftest/{self.request['id']}/logs/win-redacted.txt",
        ])
        self.assertEqual(stored, "Guest password: [REDACTED]\r\n".encode("utf-16"))

    def test_boundary_and_max_length_request_ids_publish_with_valid_selectors(self):
        for req_id in ("a" * 16, "a" * 17, "long-request-" + "x" * 35):
            with self.subTest(req_id_len=len(req_id)):
                self.request["id"] = req_id
                posix, _, ref, pin = self.publish()
                self.run_cli("bash", "-c", posix, status=0)
                self.assert_pushed_valid_result(ref, pin, expected_status="passed")
                new_sha = self.git("--git-dir", str(self.results_repo), "rev-parse", ref).strip()
                result_doc = json.loads(subprocess.check_output([
                    "git", "--git-dir", str(self.results_repo), "show",
                    f"{new_sha}:selftest/{req_id}/result.json",
                ]).decode("utf-8"))
                self.assertEqual(result_doc["id"], req_id)
                prefix = launch.owned_vm_prefix(req_id)
                expected_domain = f"ubuntu-server-amd64-{prefix}a1b2-dev-alchemy"
                self.assertEqual(result_doc["cleanup"]["owned_vms"], [expected_domain])

    def test_non_adopting_posix_retains_lock_even_with_valid_results(self):
        # Simulate macOS (non-adopting POSIX) with the exact two-snapshot reparented
        # job-control child scenario: valid artifacts must NOT release the active lock.
        wrapper_hook = self.root / "non-adopting-hook.py"
        wrapper_hook.write_text(
            "import os, sys\n"
            f"sys.path.insert(0, {str(SCRIPTS)!r})\n"
            "import launch\n"
            "real_popen = launch.subprocess.Popen\n"
            "state = {'provider_pid': 501, 'calls': 0}\n"
            "class FakeScanner:\n"
            "    def __init__(self, out):\n"
            "        self.pid = 999999\n"
            "        self.returncode = 0\n"
            "        self._out = out\n"
            "    def communicate(self, timeout=None):\n"
            "        return self._out, ''\n"
            "def patched_popen(cmd, *args, **kwargs):\n"
            "    if isinstance(cmd, list) and cmd and str(cmd[0]).endswith('ps'):\n"
            "        state['calls'] += 1\n"
            "        pid = state['provider_pid']\n"
            "        if state['calls'] == 1:\n"
            "            return FakeScanner(f'{pid} {os.getpid()} {pid} S Mon Jan  1 00:00:00 2026\\n')\n"
            "        orphan = pid + 10000\n"
            "        return FakeScanner(f'{orphan} 1 {orphan} S Mon Jan  1 00:00:01 2026\\n')\n"
            "    proc = real_popen(cmd, *args, **kwargs)\n"
            "    if kwargs.get('process_group') == 0:\n"
            "        state['provider_pid'] = proc.pid\n"
            "    return proc\n"
            "launch.sys.platform = 'darwin'\n"
            "launch.subprocess.Popen = patched_popen\n"
            "raise SystemExit(launch.main())\n",
            encoding="utf-8",
        )
        self.request["id"] = "dryrun-non-adopting"
        posix, _, ref, pin = self.publish()
        env = {**self.env, "SAILWRIGHT_HOST_AGENT_HOOK": str(wrapper_hook)}
        run = self.run_cli("bash", "-c", posix, status=0, env=env)
        self.assertIn("cannot confirm absence of reparented descendants", run.stderr)
        self.assertIn("active request lock retained", run.stderr)
        # Valid artifacts cannot clear the lock on a non-adopting POSIX path.
        self.assertTrue((self.work / "active" / "cleanup-pending").is_file())

        # Subsequent request stays blocked until operator recovery removes the lock.
        self.request["id"] = "dryrun-after-non-adopting"
        next_posix, _, next_ref, next_pin = self.publish()
        blocked = self.run_cli("bash", "-c", next_posix, status=1)
        self.assertIn("an active request lock exists", blocked.stderr)
        shutil.rmtree(self.work / "active")
        self.run_cli("bash", "-c", next_posix, status=0)
        self.assert_pushed_valid_result(next_ref, next_pin, expected_status="passed")

    def test_provider_cannot_write_extra_results_ref(self):
        posix, _, ref, pin = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "extra_ref", "FAKE_ORIGINAL_REPO": str(self.results_repo)}
        self.run_cli("bash", "-c", posix, env=env)
        self.assert_pushed_valid_result(ref, pin)
        self.assertEqual(self.git("--git-dir", str(self.results_repo), "for-each-ref",
                                  "--format=%(refname)").splitlines(), [ref])

    def test_origin_redirection_does_not_modify_either_remote(self):
        posix, _, ref, pin = self.publish()
        redirect = self.root / "redirect.git"
        self.git("clone", "--bare", str(self.results_repo), str(redirect))
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "origin_redirect", "FAKE_REDIRECT_REPO": str(redirect)}
        result = self.run_cli("bash", "-c", posix, status=1, env=env)
        self.assertIn("results Git configuration changed", result.stderr)
        self.assert_unpublished(ref, pin)
        self.assert_unpublished(ref, pin, redirect)

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
