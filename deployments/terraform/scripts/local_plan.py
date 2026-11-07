#!/usr/bin/env python3
"""Keep saved runner plans local, short-lived, and tied to their source inputs."""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[3]
TERRAFORM_ROOT = ROOT / "deployments/terraform"
UNIT = TERRAFORM_ROOT / "env/azure_dev/azure_gh_runner"
MAX_AGE_SECONDS = 15 * 60


class PlanError(Exception):
    pass


def git_commit():
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()


def source_digest():
    """Include local/ignored inputs, but never Terraform state or generated caches."""
    digest = hashlib.sha256()
    paths = [ROOT / "Makefile"]
    for directory, dirs, files in os.walk(TERRAFORM_ROOT):
        dirs[:] = sorted(d for d in dirs if d not in {
            ".terraform", ".terragrunt-cache", ".trivy-cache", "__pycache__",
        })
        paths.extend(Path(directory) / f for f in files if f.endswith(
            (".tf", ".hcl", ".tfvars", ".json", ".py", ".sh")
        ) and not f.startswith("tf.plan"))
    # find_in_parent_folders can also resolve local settings above this directory.
    paths.extend(parent / "env_local.hcl" for parent in TERRAFORM_ROOT.parents
                 if (parent / "env_local.hcl").is_file())
    for path in sorted(set(paths)):
        digest.update(os.path.relpath(path, ROOT).encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def file_digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run_terragrunt(*args, capture=False):
    result = subprocess.run(["terragrunt", *args], cwd=UNIT, check=False,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None)
    if result.returncode:
        # `show -json` and its diagnostics may contain sensitive values.
        raise PlanError("Terragrunt failed; inspect the plan locally." if capture
                        else "Terragrunt failed; generate a new plan before retrying.")
    return result.stdout


def public_address(address):
    # for_each keys can contain tenant IDs or other private input values.
    return re.sub(r'\["(?:[^"\\]|\\.)*"\]', '[*]', address)


def changes(document):
    if not isinstance(document, dict) or not str(document.get("format_version", "")).startswith("1."):
        raise PlanError("Unsupported plan JSON format.")
    if document.get("errored") or document.get("complete") is False:
        raise PlanError("Incomplete or errored plans cannot be used.")
    rows = []
    valid_actions = {("no-op",), ("read",), ("create",), ("update",), ("delete",),
                     ("delete", "create"), ("create", "delete"), ("forget",)}
    for resource in document.get("resource_changes", []):
        actions = tuple(resource["change"]["actions"])
        if actions not in valid_actions:
            raise PlanError("Unsupported resource action in plan.")
        if resource["mode"] != "managed" or actions == ("no-op",):
            continue
        rows.append((public_address(resource["address"]), actions))
    return rows


def summary(rows):
    counts = {"create": 0, "update": 0, "delete": 0}
    for _, actions in rows:
        for action in counts:
            counts[action] += action in actions
    lines = [f"Plan: {counts['create']} to add, {counts['update']} to change, {counts['delete']} to destroy."]
    lines.extend(f"- {address}: {'/'.join(actions)}" for address, actions in sorted(rows))
    return "\n".join(lines)


def check_destructive(rows):
    # Protect all managed resources, including removed prevent_destroy blocks.
    if any("delete" in actions or "forget" in actions for _, actions in rows):
        if os.environ.get("ALLOW_DESTRUCTIVE") != "1":
            raise PlanError("Plan deletes, replaces, or forgets managed resources. Review locally; "
                            "set ALLOW_DESTRUCTIVE=1 explicitly to proceed.")


class SavedPlan:
    def __init__(self, unit=None):
        unit = UNIT if unit is None else unit
        self.path = unit / "tf.plan"
        self.metadata = unit / "tf.plan.meta.json"

    def remove(self):
        self.metadata.unlink(missing_ok=True)
        self.path.unlink(missing_ok=True)

    def record(self):
        if not self.path.is_file():
            raise PlanError("Terragrunt did not create a saved plan.")
        self.path.chmod(0o600)
        data = {"commit": git_commit(), "source": source_digest(),
                "sha256": file_digest(self.path), "created": time.time()}
        self.metadata.write_text(json.dumps(data) + "\n")
        self.metadata.chmod(0o600)

    def verify(self):
        try:
            data = json.loads(self.metadata.read_text())
            age = time.time() - data["created"]
            valid = (0 <= age <= MAX_AGE_SECONDS
                     and data["commit"] == git_commit()
                     and data["source"] == source_digest()
                     and data["sha256"] == file_digest(self.path))
        except (OSError, ValueError, KeyError, TypeError):
            valid = False
        if not valid:
            raise PlanError("Saved plan missing, expired, modified, or from different inputs/commit. "
                            "Run make deploy-plan-terraform-azure-gh-runner again.")


@contextmanager
def unit_lock():
    # Serialise plan, summary and apply across local processes.
    with (UNIT / "tf.plan.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise PlanError("Another plan command is running for this unit.") from exc
        yield


def execute(command):
    saved = SavedPlan()
    with unit_lock():
        if command == "plan":
            saved.remove()
            try:
                before = (git_commit(), source_digest())
                run_terragrunt("plan", "-out", str(saved.path))
                if before != (git_commit(), source_digest()):
                    raise PlanError("Inputs changed during planning (possibly formatting hooks). "
                                    "Review changes and generate a new plan.")
                saved.record()
            except BaseException:
                saved.remove()
                raise
            return
        saved.verify()
        # Inspect and apply exactly the same private copy, even if tf.plan changes.
        with tempfile.TemporaryDirectory(prefix="runner-plan-") as directory:
            snapshot = Path(directory) / "tf.plan"
            snapshot.write_bytes(saved.path.read_bytes())
            if file_digest(snapshot) != file_digest(saved.path):
                raise PlanError("Plan changed while being read. Generate a new plan.")
            raw = run_terragrunt("show", "-json", str(snapshot), capture=True)
            try:
                rows = changes(json.loads(raw))
            except (ValueError, KeyError, TypeError) as exc:
                raise PlanError("Could not read plan JSON; no apply was attempted.") from exc
            saved.verify()
            print(summary(rows), flush=True)
            if command == "apply":
                check_destructive(rows)
                try:
                    run_terragrunt("apply", str(snapshot))
                finally:
                    # A failed/interrupted apply can already have changed state.
                    saved.remove()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["plan", "summary", "apply"])
    args = parser.parse_args()
    os.umask(0o077)
    try:
        execute(args.command)
    except (PlanError, OSError) as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
