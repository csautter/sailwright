#!/usr/bin/env python3
"""Validate, publish and prepare a Sailwright host test request."""

import argparse
import json
import math
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit

try:
    from jsonschema import Draft202012Validator
except ImportError:
    sys.exit("host-agent: install jsonschema in the Python environment used by the bootstrap")

HERE = Path(__file__).resolve().parent
SHA = re.compile(r"[0-9a-f]{40}")
ID = re.compile(r"[a-z0-9][a-z0-9-]{0,47}")
SKILL = "sailwright-host-agent"
LOCK_RECOVERY = "Verify the hook and its processes have stopped and finish cleanup before removing the lock directory."


class ProtocolError(Exception):
    pass


def git_location(location, label):
    if any(ord(char) < 32 or ord(char) == 127 for char in location):
        raise ProtocolError(f"{label} contains a control character")
    if Path(location).is_absolute():
        return location
    try:
        url = urlsplit(location)
        if (url.scheme in {"https", "ssh"} and url.hostname and url.path
                and not url.query and not url.fragment and url.password is None
                and (url.scheme == "ssh" or url.username is None)):
            return location
    except ValueError:
        pass
    raise ProtocolError(f"{label} must be an HTTPS or SSH URL without credentials, or an absolute local path")


def unique_keys(pairs):
    document = {}
    for key, value in pairs:
        if key in document:
            raise ProtocolError("duplicate JSON property")
        document[key] = value
    return document


def reject_constant(_):
    raise ProtocolError("invalid JSON number")


def finite_float(value):
    number = float(value)
    if not math.isfinite(number):
        reject_constant(value)
    return number


def validate(kind, data):
    try:
        document = json.loads(data.decode("utf-8-sig"), object_pairs_hook=unique_keys,
                              parse_constant=reject_constant, parse_float=finite_float)
    except UnicodeDecodeError as error:
        raise ProtocolError(f"{kind} must be UTF-8 JSON") from error
    except json.JSONDecodeError as error:
        raise ProtocolError(f"{kind} has invalid JSON at line {error.lineno}, column {error.colno}") from error
    except ValueError as error:
        raise ProtocolError(f"{kind} has an invalid JSON value") from error

    schema = json.loads((HERE / "schemas" / f"{kind}-v1.json").read_text(encoding="utf-8"))
    validator = Draft202012Validator(schema)
    errors = []
    for error in validator.iter_errors(document):
        # Avoid echoing values from an invalid request or report.
        if error.validator == "required":
            detail = error.message
        elif error.validator == "anyOf" and kind == "request":
            detail = "provide scenarios or test_commands"
        elif error.validator == "not" and kind == "request" and not error.path:
            detail = "human_approval is only allowed for host-changing requests"
        elif error.validator == "pattern" and error.path[-1] in {"commit", "request_commit"}:
            detail = "expected an exact 40-character lowercase Git SHA"
        elif error.validator == "pattern" and error.path[-1] == "binary_sha256":
            detail = "expected a 64-character lowercase SHA-256"
        elif error.validator in {"enum", "const", "type", "minimum"}:
            detail = f"expected {error.validator} {json.dumps(error.validator_value)}"
        else:
            detail = f"failed the {error.validator} constraint"
        errors.append(f"{error.json_path}: {detail}")
    if errors:
        raise ProtocolError(f"invalid {kind}: " + "; ".join(errors))

    if kind == "request":
        git_location(document["repo"], "source repo")
        document.setdefault("operation_class", "vm-lifecycle")
        document.setdefault("minimum_free_gib", 10)
    return document


def git_environment():
    # Select repositories by cwd or explicit arguments while preserving auth.
    config_env = {
        "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
        "GIT_ASKPASS", "GIT_TERMINAL_PROMPT", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_SSH_VARIANT",
    }
    return {key: value for key, value in os.environ.items()
            if not key.upper().startswith("GIT_") or key.upper() in config_env}


def git(*args, cwd=None, description, data=None, isolated=False):
    env = git_environment()
    # Isolate checkout from configured external filters.
    if isolated:
        env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull)
    # An empty template plus an empty per-repo hooks directory keep local
    # checkout and commit free of hooks. Explicit protocols forbid remote helpers.
    options = [
        "-c", "protocol.allow=never",
        "-c", "protocol.file.allow=always",
        "-c", "protocol.https.allow=always",
        "-c", "protocol.ssh.allow=always",
        "-c", "core.hooksPath=.git/host-agent-no-hooks",
        "-c", "submodule.recurse=false",
    ]
    result = subprocess.run(
        ["git", *options, *args], cwd=cwd, env=env, input=data,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    if result.returncode:
        detail = result.stderr.decode("utf-8", errors="replace").strip()
        raise ProtocolError(f"{description} (git exit {result.returncode}): {detail or 'no Git diagnostic'}")
    return result.stdout


def results_ref(prefix, request_id):
    if not ID.fullmatch(request_id):
        raise ProtocolError("invalid request id")
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_./-]*/", prefix):
        raise ProtocolError("branch prefix must end with / and use only letters, digits, _, ., / or -")
    ref = f"refs/heads/{prefix}{request_id}"
    git("check-ref-format", ref, description="invalid results branch")
    return ref


def init_repo(path):
    path.mkdir()
    git("init", "--quiet", "--template=", "--object-format=sha1",
        cwd=path, description="cannot initialize Git checkout", isolated=True)
    (path / ".git" / "host-agent-no-hooks").mkdir()


def fetch_exact(path, location, ref, sha, description):
    git("fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--depth=1", "--",
        location, ref, cwd=path, description=description)
    actual = git("rev-parse", "--verify", "FETCH_HEAD^{commit}", cwd=path,
                 description=description).decode("ascii").strip()
    if actual != sha:
        raise ProtocolError(f"{description}: expected {sha}, found {actual}")


def checkout(path, sha):
    # The fetched repo can contain executable files and attributes, which stay
    # inert during checkout. The configured host hook is the only launch target.
    git("checkout", "--quiet", "--detach", sha, "--", cwd=path, isolated=True,
        description=f"cannot check out exact commit {sha}")


def read_initial_request(results, sha, request_id):
    relative = f"selftest/{request_id}/request.json"
    tree = git("ls-tree", "-r", "-t", "--full-tree", "-z", sha, cwd=results,
               description="cannot inspect the pinned results tree")
    entries = []
    for entry in tree.split(b"\0")[:-1]:
        metadata, _, path = entry.partition(b"\t")
        entries.append((metadata.split()[0], path))
    expected = [(b"040000", b"selftest"), (b"040000", f"selftest/{request_id}".encode("ascii")),
                (b"100644", relative.encode("ascii"))]
    if entries != expected:
        raise ProtocolError(f"initial results tree must contain only the regular file {relative}")
    return results / relative, git("show", f"{sha}:{relative}", cwd=results,
                                   description="cannot read the pinned request.json")


def powershell_quote(value):
    return "'" + re.sub(r"['\u2018\u2019\u201a\u201b]", lambda match: match[0] * 2, value) + "'"


def start_lines(location, prefix, request_id, sha):
    arguments = ("--results-repo", location, "--branch-prefix", prefix,
                 "--id", request_id, "--request-commit", sha)
    posix = 'bash "${SAILWRIGHT_HOST_AGENT_BOOTSTRAP:?set the bootstrap directory}/start"'
    windows = "& (Join-Path $env:SAILWRIGHT_HOST_AGENT_BOOTSTRAP 'start.ps1')"
    # Options are fixed by this program. Quote every value for the target shell.
    posix_args = " ".join(f"{key} {shlex.quote(value)}" for key, value in zip(arguments[::2], arguments[1::2]))
    windows_args = " ".join(f"{key} {powershell_quote(value)}" for key, value in zip(arguments[::2], arguments[1::2]))
    return posix + " " + posix_args, windows + " " + windows_args


def publish_request(args):
    request = validate("request", args.file.read_bytes())
    location = git_location(args.results_repo, "results repo")
    ref = results_ref(args.branch_prefix, request["id"])
    with tempfile.TemporaryDirectory(prefix="sailwright-request-") as directory:
        repo = Path(directory) / "results"
        init_repo(repo)
        existing = git("ls-remote", "--refs", "--", location, ref, cwd=repo,
                       description="cannot reach results repo")
        if existing:
            raise ProtocolError(f"results branch {ref} already exists")
        relative = Path("selftest") / request["id"] / "request.json"
        (repo / relative.parent).mkdir(parents=True)
        (repo / relative).write_bytes((json.dumps(request, indent=2) + "\n").encode("utf-8"))
        git("add", "--", relative.as_posix(), cwd=repo, description="cannot stage request", isolated=True)
        git("commit", "--quiet", "-m", "Add host test request", cwd=repo,
            description="cannot commit request (configure a Git identity)")
        sha = git("rev-parse", "HEAD", cwd=repo, description="cannot identify published request").decode("ascii").strip()
        git("push", "--quiet", f"--force-with-lease={ref}:", "--", location, f"HEAD:{ref}", cwd=repo,
            description="cannot create results branch (check Git write access and request id)")
    print(*start_lines(location, args.branch_prefix, request["id"], sha), sep="\n")


def need_tool(name):
    if not shutil.which(name):
        raise ProtocolError(f"missing required tool on PATH: {name}")


def host_facts():
    os_name = platform.system().lower()
    machine = platform.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine, machine)
    return os_name, arch


def check_disk(work, minimum):
    free = shutil.disk_usage(work).free
    if free < minimum * 1024**3:
        raise ProtocolError(f"insufficient free disk in {work}: {free / 1024**3:.1f} GiB available, {minimum} GiB required")


def preflight(request, approved):
    target = request["target"]
    os_name, arch = host_facts()
    if (os_name, arch) != (target["os"], target["arch"]):
        raise ProtocolError(f"host mismatch: request needs {target['os']}/{target['arch']}, found {os_name}/{arch}")
    if request["operation_class"] == "host-changing" and not approved:
        raise ProtocolError("host-changing request requires the human operator to add --approve-host-changes")
    for name in ["go", *request.get("required_tools", [])]:
        need_tool(name)
    provider = os.environ.get("SAILWRIGHT_HOST_AGENT_PROVIDER", "")
    if provider not in {"claude", "codex"}:
        raise ProtocolError("set SAILWRIGHT_HOST_AGENT_PROVIDER to claude or codex")
    need_tool(provider)
    hook = Path(os.environ.get("SAILWRIGHT_HOST_AGENT_HOOK", ""))
    if not hook.is_absolute() or not hook.is_file():
        raise ProtocolError("set SAILWRIGHT_HOST_AGENT_HOOK to an absolute path to the trusted Python launch hook")
    return provider, hook


def start(args):
    location = git_location(args.results_repo, "results repo")
    if not SHA.fullmatch(args.request_commit):
        raise ProtocolError("--request-commit must be an exact 40-character lowercase SHA")
    ref = results_ref(args.branch_prefix, args.id)

    root = Path(os.environ.get("SAILWRIGHT_HOST_AGENT_WORK_ROOT", str(Path.home() / ".sailwright" / "host-tests")))
    if not root.is_absolute():
        raise ProtocolError("SAILWRIGHT_HOST_AGENT_WORK_ROOT must be an absolute path")
    root.mkdir(parents=True, mode=0o700, exist_ok=True)
    lock = root / "active"
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError as error:
        raise ProtocolError(f"an active request lock exists at {lock}. {LOCK_RECOVERY}") from error

    hook_running = False
    try:
        work = Path(tempfile.mkdtemp(prefix=f"{args.id}-", dir=root))
        print(f"Work directory: {work}", flush=True)
        results = work / "results"
        init_repo(results)
        fetch_exact(results, location, ref, args.request_commit,
                    "cannot fetch pinned request from results repo")
        request_path, request_data = read_initial_request(results, args.request_commit, args.id)
        request = validate("request", request_data)
        if request["id"] != args.id:
            raise ProtocolError("request id does not match the start line")
        provider, hook = preflight(request, args.approve_host_changes)
        check_disk(work, request["minimum_free_gib"])

        source = work / "source"
        init_repo(source)
        sha = request["commit"]
        fetch_exact(source, request["repo"], sha, sha, f"cannot fetch source commit {sha}")
        checkout(source, sha)
        checkout(results, args.request_commit)
        if request_path.is_symlink() or not request_path.is_file():
            raise ProtocolError("request.json in the results checkout must be a regular file")
        if request_path.read_bytes() != request_data:
            raise ProtocolError("request.json in the results checkout differs from the pinned request")
        git("remote", "add", "origin", location, cwd=results, description="cannot configure results origin")
        check_disk(work, request["minimum_free_gib"])

        command = [
            sys.executable, "-B", str(hook), "--provider", provider, "--skill", SKILL,
            "--request", str(request_path), "--source-dir", str(source), "--results-dir", str(results),
            "--results-ref", ref, "--request-commit", args.request_commit,
        ]
        if args.approve_host_changes and request["operation_class"] == "host-changing":
            command.append("--approve-host-changes")
        print(f"Ready: source {sha}, results {ref} at {args.request_commit}", flush=True)
        if args.dry_run:
            print(f"Dry run: preflight passed for {provider} with skill {SKILL}.")
            return 0
        hook_running = True
        try:
            process = subprocess.Popen(command, cwd=hook.parent, env=git_environment())
        except OSError:
            hook_running = False
            raise
        try:
            status = process.wait()
        except KeyboardInterrupt as error:
            raise ProtocolError("interrupted while waiting for the launch hook") from error
        if status < 0:
            raise ProtocolError(f"launch hook terminated by signal {-status}")
        if os.name == "nt" and status >= 0xC0000000:
            raise ProtocolError(f"launch hook terminated with Windows exception status 0x{status:08x}")
        hook_running = False
        return status
    finally:
        if hook_running:
            print(f"host-agent: active request lock retained at {lock}. {LOCK_RECOVERY}", file=sys.stderr)
        else:
            lock.rmdir()


def parse_args():
    cli = argparse.ArgumentParser(description=__doc__)
    sub = cli.add_subparsers(dest="action", required=True)
    validator = sub.add_parser("validate", help="validate a v1 request or result JSON file")
    validator.add_argument("kind", choices=["request", "result"])
    validator.add_argument("file", type=Path)
    publisher = sub.add_parser("request", help="publish a new request and print both host start commands")
    publisher.add_argument("file", type=Path)
    starter = sub.add_parser("start", help="prepare and start the configured host agent")
    starter.add_argument("--id", required=True)
    starter.add_argument("--request-commit", required=True)
    starter.add_argument("--dry-run", action="store_true")
    starter.add_argument("--approve-host-changes", action="store_true")
    for parser in [publisher, starter]:
        parser.add_argument("--results-repo", required=True)
        parser.add_argument("--branch-prefix", default="selftest/")
    return cli.parse_args()


def main():
    args = parse_args()
    try:
        if args.action == "validate":
            validate(args.kind, args.file.read_bytes())
            print(f"Valid {args.kind} v1")
            return 0
        need_tool("git")
        if args.action == "request":
            publish_request(args)
            return 0
        return start(args)
    except (ProtocolError, OSError) as error:
        print(f"host-agent: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
