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

    if behavior == "sleep":
        time.sleep(10)
        return 0

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
    overall_status = "failed" if behavior in {"report_failed", "fail_then_idle"} else "passed"
    step_status = "failed" if behavior in {"report_failed", "fail_then_idle"} else "passed"
    step_exit = 2 if behavior in {"report_failed", "fail_then_idle"} else 0

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
            "sudo",
            "-f ",
            "-f)",
            "--force*",
            " -c ",
            "--exec-path",
            "--upload-pack",
            "--yolo",
            "--dangerously-skip-permissions",
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

    def test_materialize_claude_settings_anchors_git_and_protects_trusted_dirs(self):
        base = json.loads(launch.DEFAULT_CLAUDE_SETTINGS.read_text(encoding="utf-8"))
        with tempfile.TemporaryDirectory(prefix="claude-materialize-test-") as tmp:
            root = Path(tmp).resolve()
            source_dir = root / "source"
            results_dir = root / "results"
            source_dir.mkdir()
            results_dir.mkdir()

            effective = launch.materialize_claude_settings(
                base,
                set(),
                source_dir=source_dir,
                results_dir=results_dir,
                protected_dirs=(SCRIPTS, launch.DEFAULT_SKILL_FILE.parent),
            )
            allow = effective["permissions"]["allow"]
            deny = effective["permissions"]["deny"]

            for repo_dir in (source_dir, results_dir):
                for subcmd in launch.READ_ONLY_GIT_SUBCOMMANDS:
                    self.assertIn(f"Bash(git -C {repo_dir} {subcmd})", allow)
                    self.assertIn(f"Bash(git -C {repo_dir} {subcmd} *)", allow)

            self.assertIn(f"Bash(git -C {source_dir} push)", deny)
            self.assertIn(f"Bash(git -C {source_dir} push*)", deny)
            self.assertIn(f"Bash(git -C {source_dir} * push*)", deny)

            for protected in (SCRIPTS, launch.DEFAULT_SKILL_FILE.parent):
                glob = launch.claude_abs_dir_glob(protected)
                self.assertTrue(glob.startswith("//"))
                self.assertFalse(glob.startswith("///"))
                self.assertIn(f"Edit({glob})", deny)
                self.assertIn(f"Write({glob})", deny)

            # Verify Windows path formatting avoids triple slashes and normalizes backslashes.
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

        for original in (
            'pattern = ["sailwright", "install"]',
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
        self.assertIn("Bash(sudo:*)", effective_claude["permissions"]["deny"])
        self.assertIn("Bash(*--dangerously-skip-permissions*)", effective_claude["permissions"]["deny"])

        base_rules = launch.validate_codex_rules(launch.DEFAULT_CODEX_RULES.read_text(encoding="utf-8"))
        with tempfile.TemporaryDirectory(prefix="codex-rules-test-") as tmp:
            root = Path(tmp).resolve()
            source_dir = root / "source"
            results_dir = root / "results"
            source_dir.mkdir()
            results_dir.mkdir()

            rendered_default = launch.render_codex_rules(
                base_rules, set(), source_dir=source_dir, results_dir=results_dir
            )
            parsed_default = {
                rule["pattern"]: rule["decision"] for rule in launch.validate_codex_rules(rendered_default)
            }
            self.assertEqual(parsed_default[("git", "-C", str(source_dir), "push")], "forbidden")
            for suffix in (
                ("push", "--force"),
                ("push", "-f"),
                ("push", "--force-with-lease"),
                ("push", "origin", "--force"),
                ("push", "origin", "-f"),
                ("push", "origin", "--force-with-lease"),
            ):
                self.assertEqual(parsed_default[("git", "-C", str(results_dir), *suffix)], "forbidden")
            for bin_path in (str(source_dir / "sailwright"), str(source_dir / "sailwright.exe")):
                self.assertEqual(parsed_default[(bin_path, "provision", "local")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "install")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "self-update")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "destroy", "all")], "forbidden")
                self.assertEqual(parsed_default[(bin_path, "stop", "all")], "forbidden")

            rendered_approved = launch.render_codex_rules(
                base_rules, categories, source_dir=source_dir, results_dir=results_dir
            )
            parsed_approved = {
                rule["pattern"]: rule["decision"] for rule in launch.parse_codex_rules(rendered_approved)
            }
            self.assertEqual(parsed_approved[("sailwright", "install")], "prompt")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "install")], "prompt")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright.exe"), "install")], "prompt")
            self.assertEqual(parsed_approved[("sailwright", "provision", "local")], "forbidden")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "provision", "local")], "forbidden")
            self.assertEqual(parsed_approved[(str(source_dir / "sailwright"), "destroy", "all")], "forbidden")
            self.assertEqual(parsed_approved[("git", "-C", str(source_dir), "push")], "forbidden")
            self.assertEqual(parsed_approved[("codex", "--yolo")], "forbidden")

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
        source_dir, results_dir = record["add_dirs"]
        self.assertIn(f"Bash(git -C {source_dir} rev-parse *)", record["claude_settings"]["permissions"]["allow"])
        self.assertIn(f"Bash(git -C {results_dir} status *)", record["claude_settings"]["permissions"]["allow"])
        self.assertIn(f"Bash(git -C {source_dir} push*)", record["claude_settings"]["permissions"]["deny"])
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
        source_dir, results_dir = record["add_dirs"]
        self.assertIn(json.dumps(["git", "-C", source_dir, "push"]), record["codex_rules"])
        self.assertIn(json.dumps(["git", "-C", results_dir, "push", "origin", "-f"]), record["codex_rules"])
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
        self.assertIn(f"Bash(git -C {record['add_dirs'][0]} status *)", record["claude_settings"]["permissions"]["allow"])

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

    def test_timeout_enforced_with_explicit_exit_code_124(self):
        self.request.update(id="dryrun-timeout", time_limit_seconds=1)
        posix, _, _, _ = self.publish()
        env = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "sleep"}
        run = self.run_cli("bash", "-c", posix, status=124, env=env)
        self.assertIn("timed out", run.stderr)
        self.assertFalse((self.work / "active").exists())

    def test_post_push_interactive_tui_timeout_maps_exit_code_from_result_status(self):
        self.request.update(id="dryrun-idle-pass", time_limit_seconds=2)
        posix, _, ref, pin = self.publish()
        env_pass = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "pass_then_idle"}
        run_pass = self.run_cli("bash", "-c", posix, status=0, env=env_pass)
        self.assertIn("valid pushed result (status: passed)", run_pass.stderr)
        self.assert_pushed_valid_result(ref, pin, expected_status="passed")

        self.request.update(id="dryrun-idle-fail", time_limit_seconds=2)
        posix_fail, _, ref_fail, pin_fail = self.publish()
        env_fail = {**self.env, "FAKE_PROVIDER_BEHAVIOR": "fail_then_idle"}
        run_fail = self.run_cli("bash", "-c", posix_fail, status=1, env=env_fail)
        self.assertIn("valid pushed result (status: failed)", run_fail.stderr)
        self.assert_pushed_valid_result(ref_fail, pin_fail, expected_status="failed")

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
