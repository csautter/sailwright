# Host test protocol

Use a host test when a check needs a real hypervisor or native host behavior.
Run the matching [deterministic E2E scenario](./testing-workflows.md#deterministic-e2e-scenarios)
at the lowest suitable test level first. A container agent prepares the host
request. A human reviews it and pastes one start command on the target host.

## What the host agent may and may not do

| Area | Allowed without extra approval (`vm-lifecycle`) | Requires explicit human approval (`host-changing` + `--approve-host-changes`) | Prohibited |
| --- | --- | --- | --- |
| Provider mode | Approval-preserving automatic mode (`auto` for Claude Code, with `default` supported as a fallback; `approval_policy = "on-request"` and `sandbox_mode = "workspace-write"` for Codex) | Same approval-preserving mode; scoped commands move from denied to interactive prompt (`ask`/`prompt`), never auto-bypass | `--dangerously-skip-permissions`, `bypassPermissions`, `dontAsk`, `--yolo`, `--dangerously-bypass-approvals-and-sandbox`, `approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, `--bypass-approvals` |
| Sailwright & host state | Verify pinned checkout SHA, collect host facts, build Sailwright, create/start/inspect/stop/destroy own named VMs (prefixed from request `id`), run requested scenarios and `test_commands` | `sailwright provision local`, `sailwright install`, `sailwright update` (and legacy `sailwright self-update`), system package/service changes, Windows administrator elevation (strictly within `human_approval.scope`) | Any unapproved host modification, silent privilege escalation, or unrequested edits pushed to the source repo |
| VMs & hypervisors | Disposable clones and own named VMs after a pre-create inventory confirms exact absence; mandatory cleanup (`destroy-owned-vms`) even after failures or timeouts | Occupying shared macOS runner slots only after coordinating with the human operator | Touching, stopping, modifying, or deleting existing, default, or foreign VMs; starting/modifying originals such as `tahoe-runner`; running `destroy all` or `stop all` |
| Secrets, time & Git | Redacted logs under `selftest/<id>/logs/`, schema-validated `result.json`, `report.md`, and hook-managed fast-forward publication to `--results-ref` | — | Secrets, tokens, private keys, or guest passwords in commands/reports/logs/Git; modifying `request.json`; provider Git access and nested launches; opening PRs or triggering workflows; exceeding `time_limit_seconds` without cleanup |

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
  `permissions.defaultMode: "auto"` (operators on accounts without classifier
  Auto Mode may use `"default"` in a trusted override file while keeping all
  guardrails). Build and VM lifecycle work within the request scope. Sensitive
  actions such as Tart clone/delete and `utmctl` retain interactive prompts.
  The settings deny Git, GitHub CLI, nested provider launches, unapproved
  host changes and privilege escalation. They also protect foreign VMs,
  credential stores, and the trusted hook and skill paths. The hook starts
  Claude with user settings, strict MCP configuration, canonical source and
  artifact paths, and a read-only `launch-brief.md` in an isolated workspace.
- **Codex (`codex-config.toml` + `codex-execpolicy.rules`)** uses
  `approval_policy = "on-request"`, `sandbox_mode = "workspace-write"`,
  disabled sandbox network access, `project_doc_max_bytes = 0`, and an empty
  `[mcp_servers]`. Execpolicy forbids Git, GitHub CLI, and nested provider
  commands at the executable prefix. Host-changing commands, including
  canonical source binary paths, stay forbidden without scoped approval.
  Rules also cover privilege escalation, foreign and original VM mutations,
  raw destructive `virsh` commands, and credential-store
  queries. `launch.py` prepares an isolated per-run `CODEX_HOME` containing the
  validated config and runtime rules (reusing only the operator's `auth.json`
  when present) and runs Codex with `-C` set to the isolated per-run working
  directory containing the read-only `launch-brief.md` file (passing `--`
  before the single-line prompt and canonical resolved `--add-dir` paths).

`launch.py` validates the templates **fail-closed** before every run and
refuses to start the provider if any bypass/YOLO mode is enabled, any required
deny/forbidden rule is missing, or any trusted file or provider executable
resolves inside `--source-dir` or `--results-dir`. When a request is
`host-changing` and the operator appends `--approve-host-changes`, `launch.py`
moves only the rules matching `human_approval.scope` from `deny`/`forbidden`
to interactive `ask`/`prompt` for that run. Git and nested provider commands
remain prohibited. The hook omits its Git authentication environment from the
provider and disables Git transports there. Provider rules and environment
settings govern the lane's command tools. Arbitrary native code with the
operator account's permissions is outside that boundary.

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

After the provider reports artifacts ready, close the TUI with `/exit` so
the hook can validate and publish. An interruption via `Ctrl+C` can require
lock recovery. At the request deadline, the hook stops the
provider and its descendants before inspecting the artifacts. It can still
publish a complete result after a timeout if it confirms process termination
and validates the recorded cleanup.

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
The lock remains after uncertain process termination, interruption, an
abnormal hook exit, or missing, invalid, or incomplete cleanup after provider
launch. Check that the provider and its descendants have stopped, verify the
native hypervisor inventory, and clean up only VMs owned by the request.
Then manually remove the `active` directory and its `cleanup-pending` marker
if present. The per-run `artifacts` directory holds the provider's local
output for inspection. Use a new request ID for a later run if the original
branch has advanced.

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

The hook runs the provider in approval-preserving mode and gives it a
Git-free artifact directory. The bootstrap passes the original destination
and the cleanup marker path to the hook in
`SAILWRIGHT_HOST_AGENT_RESULTS_REPO` and
`SAILWRIGHT_HOST_AGENT_LOCK_MARKER`. Custom hooks can keep the original
arguments, results checkout and `origin`; they can create the marker before
launch and remove it after confirming termination and cleanup. Legacy hooks
can continue to communicate normal exit statuses in the range 0–255.

The production hook starts a separate foreground process group on POSIX and
uses a Windows Job Object on Windows. It supervises descendants during the
run and restores the POSIX terminal to the operator. Linux adopts orphaned
provider descendants via `PR_SET_CHILD_SUBREAPER` and confirms process-tree
termination before releasing the lock. On macOS (and any POSIX platform without
kernel orphan adoption), an ordinary job-control subprocess in its own process
group can outlive the provider and be reparented between process scans, so
sampling cannot prove all descendants stopped; the hook therefore fails closed
by **retaining the active request lock** even when artifacts validate and
requiring the operator to verify native processes and hypervisor inventory
before manually removing the lock directory.

After the process tree stops, the hook verifies the request bytes, result
schema and pinned identities, report, regular artifact paths, supported text
encodings, private-key patterns across normalized text, bounded owned VM
prefixes, and canonical cleanup evidence. Supported encodings are UTF-8 for
`request.json`, `result.json`, and `report.md`, and UTF-8 or BOM-marked
UTF-16LE/UTF-16BE for files under `logs/`; unsupported or opaque binary files
are rejected, while accepted files are published byte-for-byte. The scanner
checks normalized text for PEM/PKCS/OpenSSH private keys, OpenPGP private/secret
key armor (`-----BEGIN PGP PRIVATE KEY BLOCK-----`), and PuTTY private key
headers (`PuTTY-User-Key-File-*`). For long request IDs (over 16 characters, up
to the 48-character schema limit), the hook derives one bounded,
collision-resistant owned VM selector prefix (`sw-<stem>-<10-hex-hash>-`) and
enforces the same prefix in both `launch-brief.md` and post-run verification so
Sailwright `--name` selectors stay within Sailwright's 24-character maximum
while keeping the full request ID for the results ref and `selftest/<id>/`
directory. The hook commits only that validated snapshot, as a single child of
the pinned request, in a fresh repository.
The hook publishes that one commit to the original destination and exact
`--results-ref` using the pinned request as the expected remote tip. URL
rewrites, redirects during push, and concurrent changes to the ref cause the
publication to fail. The hook uses the operator's configured Git credentials
for publication.

A published passed result normally returns `0`, a reported failed result
returns `1`, and a timed-out result returns `124`. A provider timeout without
a valid result also returns `124` and retains the lock. A normal nonzero
provider exit with a validated and published result preserves its mapped
exit status. A failed test with valid cleanup evidence can release the lock.

## Collect a result

Write the following under `selftest/<id>/` in the artifact directory.

| File | Contents |
| --- | --- |
| `request.json` | The pinned request, preserved byte for byte |
| `result.json` | Structured outcome conforming to the v1 result schema |
| `report.md` | Human-readable summary with steps, findings and cleanup |
| `logs/cleanup.txt` | Pre-create and post-destroy inventory records with commands, output and exit codes |
| `logs/` | Other redacted, request-specific logs |

See the [result example](../scripts/host-agent/examples/result.json) and
its [report](../scripts/host-agent/examples/report.md). Include the exact
source and request SHAs, host OS and version, architecture, hypervisor versions,
SHA-256 of the tested binary, step commands, exit codes and durations. Use a
null hash if a failed run never produced a binary. A skipped step has a null
exit code and zero duration. A timed-out step may have a null exit code if none
was available. Record created VM names and any remaining owned VMs. For each owned VM (or
`no-vms` when no VMs are created), record ordered `Before create: <name>` and
`After destroy: <name>` sections in `logs/cleanup.txt` using the canonical
inventory format:

- `Backend: <backend> (<connection>)` — identical across both phases, using
  `libvirt (qemu:///system)`, `libvirt (qemu:///session)`, `hyperv (local)`,
  `tart (local)`, or `utm (local)`
- `$ <command>` — matching the declared backend/connection and emitting exact
  one VM name per line (`virsh --connect <uri> list --all --name`,
  `Get-VM | Select-Object -ExpandProperty Name`, `tart list -q`, or
  `utmctl list | awk 'NR > 1 { sub(/^[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+/, ""); print }'`)
- Exact one-name-per-line inventory output, or the single line
  `[No VMs returned]` when empty (multi-column tables and surviving owned
  guests are rejected)
- `Exit code 0` at the end of each section

The hook validates the canonical shape, backend/connection match, absence of
declared owned VMs, and exit status. The records remain provider-reported
evidence; native hypervisor checks establish actual host VM state. Put
outstanding work under `open_items` and proposed fixes under `follow_up`.
Each follow-up has a proposal and reason, and a kind
of `scenario_proposal`, `skill_correction` or `issue_draft`.

Keep secrets and guest passwords out of commands, reports, logs and Git
history. Redact credentials before writing any artifact, and mark redactions
in recorded commands so they can be interpreted correctly.

The provider can run the trusted schema validator before closing.

```bash
python3 /path/to/trusted/scripts/host-agent/protocol.py validate result selftest/<id>/result.json
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
- offline template checks and evaluation with `codex execpolicy check` when
  Codex is installed, covering denied Git and nested provider commands and
  preserved scoped host-change prompts (`test_launch.py`),
- end-to-end dry runs through `scripts/host-agent/start` (Bash, Zsh) and
  `scripts/host-agent/start.ps1` (PowerShell when installed) invoking the
  production `launch.py` hook with a fake provider binary against a local bare
  results repository, verifying `result.json`, `report.md`, `logs/cleanup.txt`,
  `follow_up`, byte-for-byte `request.json` preservation, timeout handling, and
  rejection without publication of invalid artifacts and unauthorized
  destinations (`test_launch.py`).
