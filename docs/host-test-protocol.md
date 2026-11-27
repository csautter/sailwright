# Host test protocol

Use a host test when a check needs a real hypervisor or native host behavior.
Run the matching [deterministic E2E scenario](./testing-workflows.md#deterministic-e2e-scenarios)
at the lowest suitable test level first. A container agent prepares the host
request. A human reviews it and pastes one start command on the target host.

## What the host agent may and may not do

| Area | Allowed without extra approval (`vm-lifecycle`) | Requires explicit human approval (`host-changing` + `--approve-host-changes`) | Prohibited |
| --- | --- | --- | --- |
| Provider mode | Approval-preserving automatic mode (`default`/`auto` for Claude Code; `approval_policy = "on-request"` and `sandbox_mode = "workspace-write"` for Codex) | Same approval-preserving mode; scoped commands move from denied to interactive prompt (`ask`/`prompt`), never auto-bypass | `--dangerously-skip-permissions`, `bypassPermissions`, `dontAsk`, `--yolo`, `--dangerously-bypass-approvals-and-sandbox`, `approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, `--bypass-approvals` |
| Sailwright & host state | Verify pinned checkout SHA, collect host facts, build Sailwright, create/start/inspect/stop/destroy own named VMs (prefixed from request `id`), run requested scenarios and `test_commands` | `sailwright provision local`, `sailwright install`, `sailwright self-update`, system package/service changes, Windows administrator elevation (strictly within `human_approval.scope`) | Any unapproved host modification, silent privilege escalation, or unrequested edits pushed to the source repo |
| VMs & hypervisors | Disposable clones and own named VMs after a pre-create inventory confirms exact absence; mandatory cleanup (`destroy-owned-vms`) even after failures or timeouts | Occupying shared macOS runner slots only after coordinating with the human operator | Touching, stopping, modifying, or deleting existing, default, or foreign VMs; starting/modifying originals such as `tahoe-runner`; running `destroy all` or `stop all` |
| Secrets, time & Git | Redacted logs under `selftest/<id>/logs/`, schema-validated `result.json`, `report.md`, and fast-forward push of `--results-ref` only | — | Secrets, tokens, private keys, or guest passwords in commands/reports/logs/Git; modifying `request.json`; force-pushing; pushing any other ref; opening PRs or triggering workflows; exceeding `time_limit_seconds` without cleanup |

## Prerequisites

- Use Git with support for `git init --object-format=sha1` and Python 3.11 or
  newer with `jsonschema` 4.x in the container and on the host. Install the
  dependency into the Python environment you will use,
  for example `python3 -m pip install 'jsonschema>=4.18,<5'` in an activated
  virtual environment. On Windows, use native Python on PATH as `python.exe`
  and PowerShell 7.3 or newer (`pwsh`). On macOS and Linux use native
  `python3` and paste into Bash or Zsh.
- On the host, keep a **trusted** Sailwright checkout separate from any code
  under test. Point `SAILWRIGHT_HOST_AGENT_BOOTSTRAP` to its `scripts/host-agent`
  directory and `SAILWRIGHT_HOST_AGENT_HOOK` to its
  `scripts/host-agent/launch.py` production launch hook. The bootstrap and
  launch hook load `protocol.py`, `schemas/`, `templates/`, and
  `.claude/skills/sailwright-host-agent/SKILL.md` only from that trusted
  location—never from the fetched `--source-dir`.
- Install Go, the scenario's tools, and Claude Code (`claude`) or Codex CLI
  (`codex`) on the host. Set `SAILWRIGHT_HOST_AGENT_PROVIDER` to `claude` or
  `codex`, and sign in with the provider beforehand.
- Set up Git credentials out of band using a credential helper or SSH.
  The source commit must be reachable by SHA from the test host. The
  results repo must permit the container to create request branches and
  the host to read and append results. Configure a Git author identity for
  both. Use a shared HTTPS or SSH URL, or an absolute Git path for local
  fixtures on the same system.
- **Personal Mac isolation:** On a personal Mac, always run the host agent
  under a **dedicated macOS user** that has no access to personal SSH/GPG
  keys, browser profiles, or personal files. Use a disposable macOS guest
  (Tart clone) when testing `sailwright install` or `sailwright provision local`
  where practical.

## Auto-mode settings templates and launch hook

The repository ships the production launch hook
[`scripts/host-agent/launch.py`](../scripts/host-agent/launch.py), the host
skill
[`.claude/skills/sailwright-host-agent/SKILL.md`](../.claude/skills/sailwright-host-agent/SKILL.md),
and approval-preserving settings templates under
[`scripts/host-agent/templates/`](../scripts/host-agent/templates/):

- **Claude Code (`claude-settings.json`):** sets
  `enableAllProjectMcpServers: false`, `disableAllHooks: true`, and
  `permissions.defaultMode: "default"` (operators on Claude Code releases with
  classifier Auto Mode may use `"auto"` in a trusted copy while keeping all
  guardrails). Safe read, build, and `vm-lifecycle` commands are allowed;
  sensitive actions such as Tart clone/delete, `utmctl`, and Git push prompt
  interactively (`ask`); host-changing commands (`sailwright provision local`,
  `sailwright install`, `sudo`, Windows `runas`/`RunAs`), force-push, bypass
  flags, foreign/original VM mutations (`tahoe-runner`, raw `virsh undefine` /
  `destroy`), PR/workflow commands, and credential-store reads (`~/.ssh/**`,
  `~/.aws/**`, `~/.gnupg/**`, `~/.git-credentials`, Keychain) are explicitly
  denied. `launch.py` invokes `claude` with `--setting-sources user` and
  `--strict-mcp-config` from the trusted hook directory so project settings,
  hooks, and `.mcp.json` inside `--source-dir` are not activated.
- **Codex (`codex-config.toml` + `codex-execpolicy.rules`):** sets
  `approval_policy = "on-request"`, `sandbox_mode = "workspace-write"`,
  `project_doc_max_bytes = 0`, and empty `[mcp_servers]`, paired with Starlark
  `prefix_rule(..., decision = "forbidden")` rules blocking host-changing
  commands, privilege escalation, force-push, bypass flags, `tahoe-runner`
  mutations, raw `virsh undefine`/`destroy`, PR/workflow commands, and
  Keychain queries. `launch.py` prepares an isolated per-run `CODEX_HOME`
  containing the validated config and rules (reusing only the operator's
  `auth.json` when present) and runs Codex with `-C` set to the trusted hook
  directory.

`launch.py` validates the templates **fail-closed** before every run and
refuses to start the provider if any bypass/YOLO mode is enabled, any required
deny/forbidden rule is missing, or any trusted file or provider executable
resolves inside `--source-dir` or `--results-dir`. When a request is
`host-changing` and the operator appends `--approve-host-changes`, `launch.py`
moves only the rules matching `human_approval.scope` from `deny`/`forbidden`
to interactive `ask`/`prompt` for that run; bypass flags, force-push, foreign
VMs, and credential protections remain strictly denied.

Optional operator environment overrides (must be absolute paths outside
`--source-dir` and `--results-dir`):

- `SAILWRIGHT_HOST_AGENT_SKILL_FILE`: custom trusted path to `SKILL.md`
- `SAILWRIGHT_HOST_AGENT_CLAUDE_SETTINGS`: custom trusted Claude settings JSON
- `SAILWRIGHT_HOST_AGENT_CODEX_CONFIG`: custom trusted Codex TOML config
- `SAILWRIGHT_HOST_AGENT_CODEX_RULES`: custom trusted Codex execpolicy rules

## Platform notes

- **macOS (Tart and UTM):** Run under a dedicated macOS user on a personal Mac.
  Always clone into disposable Tart VMs prefixed from the request `id`; keep
  originals such as `tahoe-runner` untouched and stopped. Ask the human before
  occupying macOS runner slots (Apple Virtualization Framework limits
  concurrent macOS guests to two per host). Copy updated files into Tart guests
  over SSH/SCP rather than relying on live virtiofs updates (virtiofs can serve
  stale contents after host file replacement). Keep UTM bundles in a
  request-scoped directory (`SAILWRIGHT_UTM_VM_DIR`), use
  `boot-utm.py --software-emulation` in nested Tart runs, use `ramfb` /
  `virtio-ramfb` for QMP `screendump`, expose WinRM only on `127.0.0.1` via
  `hostfwd_add`, and remember UTM `delete` removes a registered bundle in place
  with its disk.
- **Windows (Hyper-V):** Check `Get-Service vmms`, Default Switch, memory, and
  Hyper-V Worker-Admin events as documented in
  [`.claude/skills/sailwright-host-e2e/SKILL.md#windows-hyper-v-test-host`](../.claude/skills/sailwright-host-e2e/SKILL.md#windows-hyper-v-test-host).
  Windows administrator elevation is host-changing and requires
  `operation_class: "host-changing"`, `human_approval`, and
  `--approve-host-changes`.
- **Linux (libvirt / QEMU):** Keep `DEV_ALCHEMY_LIBVIRT_URI` and
  `virsh --connect` on the exact same URI (`qemu:///system` or
  `qemu:///session`) for all lifecycle and inventory commands, and check exact
  full libvirt domain names (never substring matches).

## Create and publish a request

Copy the [v1 request example](../scripts/host-agent/examples/request.json) to
`request.json`. Use a unique lowercase ID, the source Git URL, and the full
40-character lowercase commit SHA. Set the target to `linux`, `darwin`, or
`windows` and `amd64` or `arm64`. Provide `scenarios` using scenario names
from `test/e2e/scenarios/`, explicit `test_commands`, or both. Set the time
limit in seconds and describe the work in `agent_instructions`. List any
scenario-specific executables under `required_tools`. The bootstrap checks
them on PATH and checks the work volume for `minimum_free_gib` (default 10).
Account for the space needed by images, VMs and logs.

`operation_class` defaults to `vm-lifecycle`. This covers building Sailwright
and operating disposable VMs created for this request. Give each VM a unique
name, check that it is absent before creation, and destroy only VMs created
for the request. The required cleanup rule is `destroy-owned-vms`, including
after failed steps. For anything that modifies the host outside this scope,
such as `sailwright provision local` or `sailwright install`, obtain explicit
human approval, set `operation_class` to `host-changing` and record the
decision in `human_approval.approved_by` and the permitted actions in
`human_approval.scope`. The operator must review the pinned request and
append `--approve-host-changes` to approve those actions at launch.

```bash
python3 scripts/host-agent/protocol.py validate request request.json
python3 scripts/host-agent/protocol.py request request.json \
  --results-repo https://example.org/team/host-test-results.git
```

The helper validates the [v1 request schema](../scripts/host-agent/schemas/request-v1.json)
and publishes a new branch, `selftest/<id>` by default, containing
`selftest/<id>/request.json`. Override the branch prefix with
`--branch-prefix team/selftest/`. The path inside the branch stays
`selftest/<id>/`. The helper refuses an existing ID in that branch namespace.
It prints exactly two lines to stdout, the Bash/Zsh command first and the
PowerShell command second. Each includes the results URL, branch prefix,
request ID and immutable request commit. For operator review, inspect
`selftest/<id>/request.json` at that commit in the results repository.

## Start on the matching host

### Example: starting a host test on macOS (dedicated macOS user)

Configure the trusted environment once in the dedicated macOS user's shell
(using a Python virtual environment that has `jsonschema` installed), then
paste the emitted Bash/Zsh start command:

```bash
export SAILWRIGHT_HOST_AGENT_BOOTSTRAP="/Users/sailwright-agent/trusted/sailwright/scripts/host-agent"
export SAILWRIGHT_HOST_AGENT_HOOK="${SAILWRIGHT_HOST_AGENT_BOOTSTRAP}/launch.py"
export SAILWRIGHT_HOST_AGENT_PROVIDER="claude" # or "codex"
export SAILWRIGHT_HOST_AGENT_WORK_ROOT="/Users/sailwright-agent/.sailwright/host-tests"

# Paste the emitted start command (append --dry-run first to verify preflight):
bash "${SAILWRIGHT_HOST_AGENT_BOOTSTRAP:?set the bootstrap directory}/start" \
  --results-repo https://example.org/team/host-test-results.git \
  --branch-prefix selftest/ \
  --id macos-utm-smoke \
  --request-commit 89abcdef0123456789abcdef0123456789abcdef
```

On Windows (PowerShell 7.3+), set the matching `$env:` variables before pasting
the emitted PowerShell command:

```powershell
$env:SAILWRIGHT_HOST_AGENT_BOOTSTRAP = 'C:\trusted\sailwright\scripts\host-agent'
$env:SAILWRIGHT_HOST_AGENT_HOOK = Join-Path $env:SAILWRIGHT_HOST_AGENT_BOOTSTRAP 'launch.py'
$env:SAILWRIGHT_HOST_AGENT_PROVIDER = 'claude' # or 'codex'
```

Append `--dry-run` to perform the complete preflight and stop before launching
the hook. Dry-run requires the same tools and configuration as a real launch,
including the hook path and any `--approve-host-changes` flag. It leaves the
local checkouts available for inspection.

The bootstrap reserves a single active request in the configured work root,
fetches the pinned request from the results repo, and validates it. It checks
host OS and architecture, tool availability, host-change approval and free
disk space, then fetches and checks out the exact source SHA. It prepares a
separate detached checkout of the results commit with `origin` set to the
results repo. The pinned results tree must contain only the regular
`selftest/<id>/request.json` file, so the report, result and log paths start
empty. Request commands and instructions are data for the hook. Use a new
request ID if the branch has moved since the command was generated.

The default work root is `~/.sailwright/host-tests`. Set
`SAILWRIGHT_HOST_AGENT_WORK_ROOT` to an absolute, user-owned directory to
move it to a volume with suitable space. Use one consistent work root and
one designated account per host so the `active` lock excludes other requests.
The bootstrap prints the per-run directory and keeps it for inspection.
If the hook terminates by signal, returns a Windows exit status outside 0–255,
or the starter is interrupted while waiting, the lock remains. Verify that the
previous hook and its processes have stopped and resolve any outstanding cleanup
before removing the empty `active` directory. Remove old run directories when
their reports have been collected.

## Launch hook contract

The bootstrap invokes the configured hook (`scripts/host-agent/launch.py` by
default) with its Python interpreter and uses the directory containing the hook
as the working directory. For Git operations, use the supplied checkout paths
explicitly, for example `git -C "$results_dir"`. The hook environment retains
the operator's Git configuration files and SSH or askpass settings and clears
inherited repository, worktree and index overrides. The bootstrap passes these
arguments as individual tokens:

```text
--provider claude|codex
--skill sailwright-host-agent
--request <absolute path to selftest/<id>/request.json>
--source-dir <absolute path to detached source checkout>
--results-dir <absolute path to detached results checkout>
--results-ref refs/heads/<branch-prefix><id>
--request-commit <full request commit SHA>
[--approve-host-changes]
```

The hook enforces:

- approval-preserving automatic mode (never bypass or YOLO modes),
- trusted resolution of the skill, settings templates, `protocol.py`, and
  provider executable outside `--source-dir` and `--results-dir`,
- `operation_class`, `human_approval`, `--approve-host-changes`, and
  `time_limit_seconds`,
- foreground execution returning an explicit exit status in `0–255` (`0` for a
  validated pushed `passed` result, `124` for timeout, `1–255` for reported or
  verification failures; signals or out-of-range Windows statuses retain the
  `active` lock for manual recovery),
- post-run verification that `selftest/<id>/request.json` is byte-for-byte
  unchanged, `selftest/<id>/result.json` passes `protocol.py validate result`
  with matching `id`, `commit`, and `request_commit`, `report.md` exists, no
  symlinks or private-key blocks were written, and only `--results-ref` was
  fast-forward pushed to `origin`.

## Collect a result

Commit the following under `selftest/<id>/` in the provided results checkout.

| File | Contents |
| --- | --- |
| `request.json` | The pinned request, preserved byte for byte |
| `result.json` | Structured outcome conforming to the v1 result schema |
| `report.md` | Human-readable summary with steps, findings and cleanup |
| `logs/` | Redacted, request-specific logs referenced by the report or cleanup evidence |

See the [result example](../scripts/host-agent/examples/result.json) and
its [report](../scripts/host-agent/examples/report.md). Include the exact
source and request SHAs, host OS and version, architecture, hypervisor versions,
SHA-256 of the tested binary, step commands, exit codes and durations. Use a
null hash if a failed run never produced a binary. A skipped step has a null
exit code and zero duration. A timed-out step may have a null exit code if none
was available. Record created VM names, remaining owned VMs, and evidence from
a post-cleanup inventory. Put outstanding work under `open_items` and proposed
fixes under `follow_up`. Each follow-up has a proposal and reason, and a kind
of `scenario_proposal`, `skill_correction` or `issue_draft`.

Keep secrets and guest passwords out of commands, reports, logs and Git
history. Redact credentials before writing any artifact, and mark redactions
in recorded commands so they can be interpreted correctly.

Validate against the [v1 result schema](../scripts/host-agent/schemas/result-v1.json)
before committing. Push only the single results ref passed to the hook, with
a regular fast-forward push. A concurrent update requires reconciliation.
These example commands use the default prefix and the example request ID.

```bash
python3 /path/to/trusted/scripts/host-agent/protocol.py validate result selftest/linux-lifecycle/result.json
git push origin HEAD:refs/heads/selftest/linux-lifecycle
```

Read the completed branch from the configured results repo and match
`request_commit`, `commit` and `id` in `result.json` to the request before
consuming the report.

## Test the protocol and hook without a host run

```bash
python3 -B -m unittest discover -s scripts/host-agent/tests -v
shellcheck scripts/host-agent/start
```

The host-free test suite creates temporary local bare Git repositories and
exercises:

- request/result schemas, emitted Bash/Zsh/PowerShell start commands,
  preflight errors, and lock recovery (`test_protocol.py`),
- offline template guardrail checks confirming forbidden-permission settings
  are present in both Claude Code and Codex templates and that bypass/YOLO
  settings or missing deny rules are rejected (`test_launch.py`),
- end-to-end dry runs through `scripts/host-agent/start` (Bash, Zsh) and
  `scripts/host-agent/start.ps1` (PowerShell when installed) invoking the
  production `launch.py` hook with a fake provider binary against a local bare
  results repository, verifying `result.json`, `report.md`, `logs/cleanup.txt`,
  `follow_up`, byte-for-byte `request.json` preservation, timeout handling, and
  fail-closed rejection of tampered or unpushed results (`test_launch.py`).
