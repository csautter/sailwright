# Release testing runbook

How to run the [release test kit](../test/release/README.md) against a release
candidate, and what earlier rounds taught us. The kit README describes the
phases and helpers; this page covers the working protocol, where each kind of
check must run, and the pitfalls that cost time before.

The kit complements, and does not replace, the unit tests and the
[deterministic E2E scenarios](testing-workflows.md#deterministic-e2e-scenarios).
Full VM lifecycle runs stay opt-in by design
([ADR 0007](adr/0007-manual-host-e2e-accepted-risk.md)); this runbook is how a
person or an agent performs them.

> The kit behavior described below (invocation settings overriding `release.env`,
> gateway directories outside the repository, case-insensitive UTM UUID
> checks) comes with the fixes in
> [#317](https://github.com/csautter/sailwright/pull/317). Until that is merged,
> older kit revisions behave differently.

## Where each check runs

| Goal | Where | Why |
| --- | --- | --- |
| Installers, `provision-local`, "fresh machine" checks (Homebrew, Java) | **Disposable VM only** (`RT_DISPOSABLE=1`) | They change the system and cannot be repeated on a used host. Never run them on a developer or operator machine. |
| VM lifecycle, builds, gateway, interrupts | **Physical host** with real hypervisors | A guest without hardware virtualization skips most of them. |
| Kit logic, parsers, offline helper tests, `cli`, parts of `preflight` | **Anywhere** | No host effects. |

A candidate is only fully covered when both kinds have run. Record which plans
were not run instead of leaving the gap implicit.

## Protocol

1. **One phase per approval.** Show the exact command, run it, read
   `summary.md`, and only then ask for the next phase. Never combine phases
   that change the host.
2. **Fresh run directory for every rerun** (`RT_RUN_DIR`). Do not overwrite the
   evidence of a failed round; a fix is only credible next to the failure it
   fixes.
3. **Order:** `preflight`, `kit`, `cli`, then `interrupt`/`build`, `vm`,
   `gateway`, and always `cleanup` and `report` last.
4. **Triage before editing.** For every FAIL, open the single check log and
   decide: product bug, or kit/test assumption. Fix the cause, add a
   regression test where the product changed, and rerun the same phase.
5. **Rebuild after product changes.** `RT_VERSION` must name the commit the
   binaries were built from. Commits that only touch tests or the kit do not
   invalidate earlier product evidence; say so in the report.
6. **Do not weaken gates.** If `preflight` reports a path difference against
   the candidate branch because you are testing a fix branch, record it as
   such. Do not relax the check and do not call the result a pass for the
   unchanged candidate.
7. **Manual checks stay open until a human answers.** Collect the phase's
   `manual.txt` instructions in one message, wait, and record the answer
   verbatim. Never convert an unanswered or unrecalled check into a pass.
8. **Report.** Link every historical FAIL to the rerun that passes, list open
   items explicitly, and state whether the run is a validation of unchanged
   code or of a fix branch.

## Pitfalls seen so far

### Kit and settings

- Invocation settings win over `release.env`, including explicitly empty
  values (for example `RT_PULL_REF=` for a Tart pass). Use a separate
  `RT_RUN_DIR` and `RT_APPDATA` for a pass with a different target.
- Hypervisor VM names are global even though app data is isolated. Check what
  already exists, and never delete VMs the run did not create unless the owner
  said so explicitly.
- Do not kill processes by name. Filter by the run directory; a helper that
  matches its own `awk`/`grep` will cancel the run it is checking.
- Give long waits explicit deadlines. Packer and OCI steps can exceed the
  default check timeout.
- A PTY needs a real size (24x120). With 0x0 the progress bars never render
  and "wait for output" steps hang.
- Bash starts background helpers with SIGINT/SIGQUIT ignored, and children
  inherit that. A child that must react to Ctrl+C has to reset both to default.
- `jobs`/`wait` diagnostics from Bash can leak the sampler's source text into
  the next check. Reap background samplers explicitly.
- Do not time lock contention with a fixed sleep. The kit starts the competing
  command only after the holder record of the first one appears in the lock
  file, with an immediate lock timeout. Tart's `stop` (and sometimes Hyper-V's,
  about 3.6 s) can finish before that; VM-20 then reports `INCONCLUSIVE` as a
  skip, not a pass. Cover lock contention on a slower backend and record the
  limitation.
- A check with a skip path can pass vacuously. State in the check text what a
  skip proves.

### UTM

- UTM's preference file can still list a registration for a moment after
  `delete`, while scripting no longer has it. Treat a short window as
  synchronization, not as a stuck orphan.
- UTM UUIDs are lower case in the CLI and upper case in `utmctl`; compare
  case-insensitively.
- UTM bundles live in the container's `Documents` directory, not under
  `DEV_ALCHEMY_APP_DATA_DIR`. A failed run can leave a bundle there; check
  before the next round.
- UTM templates use bridged networking. Lifecycle checks need
  `RT_ALLOW_INSECURE_CREDENTIALS=1`, and every `create`, `start` and
  `provision` step of a scenario must pass the flag.

### Gateway

- The gateway refuses a state directory inside any git checkout, and its agent
  directory must not overlap the state directory. Create both outside the
  repository, with a short path (Unix socket paths are length-limited), and copy
  the evidence back afterwards (`gateway-evidence/` in the phase directory).
- On Windows, stop the gateway's whole process tree and wait for it to exit
  before starting the next gateway on the same state directory.
- Without a terminal, `--no-approvals` cannot confirm the binary
  interactively; pass `--binary-sha256`.
- Approval of VMs the gateway did not create applies in both `shared` and
  `isolated` mode (VM names are global). Releases before 0.27.0 skipped it
  with `isolated`, which a gateway with a legacy `data/app` directory selects
  by default; on such a build a "no approval needed" result says nothing about
  the feature.
- A grant belongs to the VM instance it was given for. After a recreate, the
  tunnel is refused again until `vm_info` is approved again.
- The typed `vm_create`, `vm_start` and `vm_provision` tools do not take the
  guest credential override. For a bridged target go through `run_cli`, which
  needs host approval (automatic only under `--bypass-approvals`).
- A tunnel request right after a recreate waits for the new guest to get an
  address and can outlast the client deadline. Wait for `vm info` through the
  CLI before probing for a refusal.
- Only an operator at a foreground terminal can approve. Say which request id
  to type, then verify in the audit log that the decision was recorded after
  the request, not just that the person replied.
- Check which `sailwright-gateway` is on `PATH`. An older installed build may
  lack subcommands such as `vm tunnel`; use the one under `dist/`.
- Hosts that run a gateway for MCP clients need `SAILWRIGHT_GATEWAY_DIR` set;
  the default `/run/sailwright-gateway` only exists in a devcontainer.

### CI interplay

- Branch CI that downloads large files from an external cache can exhaust its
  job timeout without any test failing. Read the failing step before blaming
  the change.

## Adding or changing a check

- Prefer a YAML scenario under `test/e2e/scenarios`; add a phase check only for
  what a scenario cannot express (see the kit README).
- Wait for the condition you need (a prompt, an address, a banner), not for a
  fixed sleep, unless the sleep is the thing under test.
- Make the check fail for the right reason: match the specific message, and
  assert that a skipped path is the intended refusal.
- When a scenario creates a VM, it must clean up in `always` steps and refuse to
  start if the target already exists.
- Add a regression test for every product fix found by a release run, and a
  unit test for every kit helper change that can be exercised offline
  (`python3 -I -m unittest discover -s test/release/tests -v`).
- Keep secrets out of logs. Guest passwords are redacted by the kit; do not add
  commands that print them.
