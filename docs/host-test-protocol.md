# Host test protocol

Use a host test when a check needs a real hypervisor or native host behavior.
Run the matching [deterministic E2E scenario](./testing-workflows.md#deterministic-e2e-scenarios)
at the lowest suitable test level first. A container agent prepares the host
request. A human reviews it and pastes one start command on the target host.

## Prerequisites

- Use Git with support for `git init --object-format=sha1` and Python 3.11 or
  newer with `jsonschema` 4.x in the container and on the host. Install the
  dependency into the Python environment you will use,
  for example `python3 -m pip install 'jsonschema>=4.18,<5'` in an activated
  virtual environment. On Windows, use native Python on PATH as `python.exe`
  and PowerShell 7.3 or newer (`pwsh`). On macOS and Linux use native
  `python3` and paste into Bash or Zsh.
- On the host, set `SAILWRIGHT_HOST_AGENT_BOOTSTRAP` to the absolute path to
  `scripts/host-agent` in a trusted, existing checkout that includes this
  protocol version. Keep that checkout separate from code under test. The
  start command uses those local scripts and schemas.
- Install Go, the scenario's tools, and the selected Claude Code or Codex CLI
  on the host. Set `SAILWRIGHT_HOST_AGENT_PROVIDER` to `claude` or `codex`
  and `SAILWRIGHT_HOST_AGENT_HOOK` to the absolute path of the trusted Python
  launch hook in an operator-controlled directory outside the checkouts under
  test. Trust the startup configuration in that directory and its ancestors.
  Sign in with the provider and configure its approval-preserving automatic
  mode beforehand.
- Set up Git credentials out of band using a credential helper or SSH.
  The source commit must be reachable by SHA from the test host. The
  results repo must permit the container to create request branches and
  the host to read and append results. Configure a Git author identity for
  both. Use a shared HTTPS or SSH URL, or an absolute Git path for local
  fixtures on the same system.
- Run under a dedicated macOS user on a personal Mac. Use a disposable host
  where practical, especially for installation or local provisioning.

For Bash or Zsh, set the paths with `export` before pasting a command. For
PowerShell, set them with `$env:SAILWRIGHT_HOST_AGENT_BOOTSTRAP = 'C:\trusted\sailwright\scripts\host-agent'`
and the same `$env:` syntax for the provider and hook variables.

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

Paste the matching line. Append `--dry-run` to perform the complete preflight
and stop before launching the hook. Dry-run requires the same tools and
configuration as a real launch, including the hook path and any approval
flag. It leaves the local checkouts available for inspection.

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
If the hook terminates by signal or the starter is interrupted while waiting,
the lock remains. Verify that the previous hook and its processes have
stopped and resolve any outstanding cleanup before removing the empty
`active` directory. Remove old run directories when their reports have been
collected.

## Launch hook contract

The operator supplies the hook. The bootstrap invokes it with its Python
interpreter and uses the directory containing the configured hook as the
working directory. For Git operations, use the supplied checkout paths
explicitly, for example `git -C "$results_dir"`. The hook environment retains
the operator's Git configuration files and SSH or askpass settings and
clears inherited repository, worktree and index overrides. The bootstrap
passes these arguments as individual tokens.

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

The hook must invoke `sailwright-host-agent` with the selected provider's
supported automatic mode and retain normal approval prompts and permission
boundaries. Bypass and unrestricted modes are prohibited. Launch the provider
from the trusted hook directory. Resolve the provider executable, skill,
startup settings, hooks and MCP servers only from operator-trusted locations
and configuration. Keep provider startup and configuration discovery outside
the fetched source. Pass the source path as data and prevent project settings,
hooks and servers in that checkout from being activated when granting access
to it. Stop the launch if the installed provider cannot enforce this contract.

The hook and skill configuration are supplied separately. The hook must
enforce the request's operation class, explicit approval scope and time limit,
performing cleanup even after errors or timeout. It must remain in the
foreground through cleanup and result publication, then return an explicit
exit status. Use 0 for success and 1–255 for a reported failure. A signal
or a Windows exception exit status leaves the lock in place for manual
recovery. If the request omits `operation_class`, read it as `vm-lifecycle`.

Treat commands, checkout content and free-form instructions as untrusted
input. Check their scope before running anything. Preserve existing VMs and
host configuration outside the reviewed approval. Use provider settings
appropriate to the installed CLI version and the designated run account.

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
SHA-256 of the tested binary, step
commands, exit codes and durations. Use a null hash if a failed run never
produced a binary. A skipped step has a null exit code and zero duration.
A timed-out step may have a null exit code if none was available. Record
created VM names, remaining owned VMs, and evidence from a post-cleanup
inventory. Put outstanding work under `open_items` and proposed fixes under
`follow_up`. Each follow-up has a proposal and reason, and a kind of
`scenario_proposal`, `skill_correction` or `issue_draft`.

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

## Test the protocol without a host run

```bash
python3 -B -m unittest discover -s scripts/host-agent/tests -v
shellcheck scripts/host-agent/start
```

The tests create temporary local bare repositories, exercise the emitted
commands and verify the detached checkouts. They include Bash and, when
installed, Zsh and PowerShell. The dry-run fixtures verify that the hook
stays idle. Other fixtures verify the hook's working directory, arguments,
Git environment, exit status and lock recovery. They also verify that
unexpected results paths are rejected before checkout or hook launch.
