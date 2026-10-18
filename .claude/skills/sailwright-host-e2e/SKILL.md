---
name: sailwright-host-e2e
description: Test sailwright on the real host from a devcontainer through sailwright-gateway (MCP). Use when asked to run E2E or host tests, validate VM lifecycle or local provisioning on Linux/macOS/Windows hosts, reproduce a host-specific bug, or turn a manual host check into a deterministic scenario.
---

# Sailwright host E2E testing through the gateway

You run inside a devcontainer. You have **no shell on the host**. The only way
to touch the host is the `sailwright-host` MCP server (`sailwright-gateway`).
Read `docs/agent-gateway.md` once if you have not seen it.

## Ground rules

- Never try to reach the host another way (SSH, Docker socket, mounted
  paths). If the gateway is not available, stop and tell the user.
- Safe tools run immediately. `provision_local`, `install` and any `run_cli`
  call outside the safe set wait for a human to type `approve <id>` on the
  host. Always fill `reason` with one plain sentence explaining why you
  need the call, and tell the user which job id waits for approval.
- Only one job runs at a time. Poll `job_status` (every few seconds for
  short jobs, every 30–60 s for builds) and read `job_output` from
  `next_offset`.
- Every VM you create must be destroyed again before you finish, even after
  failures.
- The binary under test is pinned by the human. If you changed sailwright
  code, ask the user to rebuild on the host and restart the gateway before
  testing it; report the `binary_sha256` you tested.

## Workflow

1. **Preflight.** If the `sailwright-host` tools are missing, run
   `sailwright-gateway connect --check` and report its output; usually the
   gateway is not running on the host or `/run/sailwright-gateway` is not
   mounted. Then call `gateway_info`. Note host OS/arch, `binary_sha256`,
   `approval_enabled`, available targets and `target_errors` (missing
   hypervisor tools show up there).
2. **Pick targets.** Use `list_targets` (`create` or `start`) and choose
   stable targets for this host, e.g. `ubuntu/server/amd64`.
3. **Run the deterministic scenarios first** from the repository root:

   ```bash
   SAILWRIGHT_E2E_EXECUTOR=gateway go test -tags e2e ./test/e2e \
     -run 'TestScenarios/smoke' -count=1 -v
   SAILWRIGHT_E2E_EXECUTOR=gateway SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64 \
     go test -tags e2e ./test/e2e -run 'TestScenarios/(vm-lifecycle|destroy-idempotent)' \
     -count=1 -timeout 6h -v
   ```

   `local-check` needs an approval on the host; run it only when asked to
   validate local provisioning, and tell the user to watch the gateway
   terminal. Builds can take hours; run long scenarios in the background
   and keep the user informed.
4. **Explore only when needed.** Use the typed tools (`vm_*`,
   `vm_provision`, `artifact_pull`) for anything the scenarios did not
   cover, or to narrow down a failure. Use `run_cli` with `bundle_root` for
   files from your workspace (absolute paths below `bundle_root`); expect an
   approval prompt.
5. **Clean up.** `list_targets` for `destroy`; every target you touched must
   show `state: missing`. Cancel jobs you started and no longer need.
6. **Report.** Summarise per scenario and step: host OS/arch, binary hash,
   target, pass/fail, duration and the relevant output lines. Reports are in
   `test/e2e/out/<scenario>.md`. Separate facts you observed from guesses.

## Turning findings into scenarios

When you found a check worth repeating, add a YAML scenario under
`test/e2e/scenarios/` instead of only describing it (format in
`docs/testing-workflows.md#writing-scenarios`). Keep it deterministic: fixed
steps, explicit expectations, cleanup in `always`. Verify it with the fake
CLI self-test (`go test ./test/e2e/...`) and then once against the gateway.
A human reviews and merges it.
