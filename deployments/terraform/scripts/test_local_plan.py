"""Exercise plan provenance and apply decisions without contacting Terraform or Azure."""

from contextlib import redirect_stdout
import io
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

import local_plan as plan


def document(actions=("update",), address='azurerm_storage_account.example', mode="managed"):
    return {"format_version": "1.2", "complete": True, "resource_changes": [{
        "address": address, "mode": mode,
        "change": {"actions": list(actions), "before": {"secret": "PRIVATE-VALUE"},
                   "after": {"id": "PRIVATE-ID"}},
    }]}


class PlanTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.tf = self.root / "deployments/terraform"
        self.unit = self.tf / "env/azure_dev/azure_gh_runner"
        self.unit.mkdir(parents=True)
        (self.root / "Makefile").write_text("# targets\n")
        self.source = self.tf / "main.tf"
        self.source.write_text('resource "azurerm_storage_account" "example" {}\n')
        for name, value in [("ROOT", self.root), ("TERRAFORM_ROOT", self.tf), ("UNIT", self.unit)]:
            self.enterContext(patch.object(plan, name, value))
        self.commit = self.enterContext(patch.object(plan, "git_commit", return_value="commit-a"))
        self.enterContext(patch.dict(os.environ, {}, clear=True))
        self.saved = plan.SavedPlan(self.unit)
        self.saved.path.write_bytes(b"saved binary plan")
        self.saved.record()
        self.output = self.enterContext(redirect_stdout(io.StringIO()))

    def execute(self, command, data=None, fail_apply=False):
        def fake(*args, capture=False):
            if args[0] == "show":
                self.assertTrue(capture)
                self.assertEqual(Path(args[-1]).read_bytes(), b"saved binary plan")
                return json.dumps(data or document()).encode()
            if args[0] == "apply":
                self.assertEqual(Path(args[-1]).read_bytes(), b"saved binary plan")
                if fail_apply:
                    raise plan.PlanError("simulated apply failure")
        with patch.object(plan, "run_terragrunt", side_effect=fake) as tool:
            plan.execute(command)
            return tool

    def test_valid_saved_plan(self):
        self.saved.verify()

    def test_missing_plan_or_metadata_refused(self):
        for path in [self.saved.path, self.saved.metadata]:
            with self.subTest(path=path.name):
                data = path.read_bytes()
                path.unlink()
                with self.assertRaises(plan.PlanError):
                    self.saved.verify()
                path.write_bytes(data)

    def test_expired_future_or_corrupt_metadata_refused(self):
        data = json.loads(self.saved.metadata.read_text())
        for created in [time.time() - 901, time.time() + 60, "bad"]:
            with self.subTest(created=created):
                self.saved.metadata.write_text(json.dumps({**data, "created": created}))
                with self.assertRaises(plan.PlanError):
                    self.saved.verify()
        self.saved.metadata.write_text("not json")
        with self.assertRaises(plan.PlanError):
            self.saved.verify()

    def test_different_commit_or_plan_bytes_refused(self):
        self.commit.return_value = "commit-b"
        with self.assertRaises(plan.PlanError):
            self.saved.verify()
        self.commit.return_value = "commit-a"
        self.saved.path.write_bytes(b"modified")
        with self.assertRaises(plan.PlanError):
            self.saved.verify()

    def test_changed_or_new_local_inputs_refused(self):
        for path in [self.source, self.tf / "env_local.hcl", self.tf / "local.auto.tfvars.json"]:
            with self.subTest(path=path.name):
                self.saved.record()
                path.write_text("changed input")
                with self.assertRaises(plan.PlanError):
                    self.saved.verify()

    def test_parent_local_settings_included(self):
        parent_settings = self.root / "env_local.hcl"
        parent_settings.write_text("local settings")
        with self.assertRaises(plan.PlanError):
            self.saved.verify()

    def test_removed_inputs_refused_but_state_and_caches_ignored(self):
        (self.tf / ".terraform").mkdir()
        (self.tf / ".terraform/generated.tf").write_text("generated")
        (self.tf / "terraform.tfstate").write_text("state")
        self.saved.verify()
        self.source.unlink()
        with self.assertRaises(plan.PlanError):
            self.saved.verify()

    def test_summary_never_applies_or_leaks_values_or_string_keys(self):
        tool = self.execute("summary", document(address='azurerm_storage_account.example["PRIVATE-KEY"]'))
        self.assertEqual(tool.call_count, 1)
        self.assertTrue(self.saved.path.exists())
        self.assertEqual(self.output.getvalue(),
                         "Plan: 0 to add, 1 to change, 0 to destroy.\n"
                         "- azurerm_storage_account.example[*]: update\n")

    def test_deletions_replacements_and_forget_blocked(self):
        for kind in ["azuread_application", "azurerm_role_assignment", "azurerm_role_definition",
                     "azurerm_key_vault", "azurerm_storage_account", "azurerm_resource_group"]:
            for actions in [("delete",), ("delete", "create"), ("create", "delete"), ("forget",)]:
                with self.subTest(kind=kind, actions=actions):
                    with self.assertRaises(plan.PlanError), patch.object(plan, "run_terragrunt") as tool:
                        tool.return_value = json.dumps(document(actions, f"{kind}.example")).encode()
                        plan.execute("apply")
                    self.assertEqual(tool.call_count, 1)  # show only
                    self.assertTrue(self.saved.path.exists())

    def test_explicit_override_allows_delete_and_cleans_up(self):
        os.environ["ALLOW_DESTRUCTIVE"] = "1"
        self.execute("apply", document(("delete",)))
        self.assertFalse(self.saved.path.exists())
        self.assertFalse(self.saved.metadata.exists())

    def test_update_allowed_and_failed_apply_also_removes_plan(self):
        with self.assertRaisesRegex(plan.PlanError, "simulated"):
            self.execute("apply", fail_apply=True)
        self.assertFalse(self.saved.path.exists())
        self.assertFalse(self.saved.metadata.exists())

    def test_invalid_or_incomplete_json_never_applies(self):
        for data in [b"broken", b'{}', json.dumps({**document(), "complete": False}).encode(),
                     json.dumps(document(("unknown",))).encode()]:
            with self.subTest(data=data), patch.object(plan, "run_terragrunt", return_value=data) as tool:
                with self.assertRaises(plan.PlanError):
                    plan.execute("apply")
                self.assertEqual(tool.call_count, 1)

    def test_failed_show_never_applies(self):
        with patch.object(plan, "run_terragrunt", side_effect=plan.PlanError("show failed")) as tool:
            with self.assertRaises(plan.PlanError):
                plan.execute("apply")
            self.assertEqual(tool.call_count, 1)

    def test_failed_replan_invalidates_previous_plan(self):
        with patch.object(plan, "run_terragrunt", side_effect=plan.PlanError("plan failed")):
            with self.assertRaises(plan.PlanError):
                plan.execute("plan")
        self.assertFalse(self.saved.path.exists())
        self.assertFalse(self.saved.metadata.exists())

    def test_new_plan_records_provenance(self):
        def fake(*args):
            self.assertEqual(args[0:2], ("plan", "-out"))
            Path(args[2]).write_bytes(b"new plan")
        with patch.object(plan, "run_terragrunt", side_effect=fake):
            plan.execute("plan")
        self.saved.verify()
        self.assertEqual(self.saved.path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.saved.metadata.stat().st_mode & 0o777, 0o600)

    def test_inputs_changed_during_plan_require_new_plan(self):
        def fake(*args):
            Path(args[2]).write_bytes(b"new plan")
            self.source.write_text("changed by formatter")
        with patch.object(plan, "run_terragrunt", side_effect=fake):
            with self.assertRaisesRegex(plan.PlanError, "Inputs changed"):
                plan.execute("plan")
        self.assertFalse(self.saved.path.exists())

    def test_input_change_during_show_never_applies(self):
        def fake(*args, **kwargs):
            self.source.write_text("changed while inspecting")
            return json.dumps(document()).encode()
        with patch.object(plan, "run_terragrunt", side_effect=fake) as tool:
            with self.assertRaises(plan.PlanError):
                plan.execute("apply")
            self.assertEqual(tool.call_count, 1)

    def test_parallel_plan_command_refused(self):
        with plan.unit_lock():
            with self.assertRaisesRegex(plan.PlanError, "Another plan"):
                plan.execute("summary")

    def test_replacements_count_as_add_and_destroy(self):
        text = plan.summary(plan.changes(document(("create", "delete"))))
        self.assertIn("1 to add, 0 to change, 1 to destroy", text)

    def test_data_source_reads_and_noop_do_not_enter_summary(self):
        self.assertEqual(plan.changes(document(("read",), mode="data")), [])
        self.assertEqual(plan.changes(document(("no-op",))), [])


if __name__ == "__main__":
    unittest.main()
