---
name: sailwright-host-e2e
description: Test sailwright through sailwright-gateway from a devcontainer or an authorized development VM (MCP). Use when asked to run E2E or host tests, validate VM lifecycle or local provisioning on Linux/macOS/Windows hosts, reproduce a host-specific bug, or turn a manual host check into a deterministic scenario.
---

# Sailwright host E2E testing through the gateway

Determine the topology from the session. In a devcontainer you have **no shell
on its host**; use the `sailwright-host` MCP server (`sailwright-gateway`) for
host operations. On a dedicated development VM where the user explicitly
authorizes local setup, agent and gateway may run in that same VM. You may
build, pin and start the local gateway there and use a local shell for setup
and capability probes. This gives no access or authorization to the outer
hypervisor host. Keep guest lifecycle operations on the gateway.
Read `docs/agent-gateway.md` once if you have not seen it.

## Ground rules

- In a devcontainer, never try to reach its host another way (SSH, Docker
  socket, mounted paths). If that host gateway is unavailable, stop and tell
  the user. In an explicitly authorized development VM, start its local
  gateway if needed; do not apply that permission to a container's host.
- Safe tools run immediately. `provision_local`, `install` and any `run_cli`
  call outside the safe set wait for a human to type `approve <id>` on the
  host. Always fill `reason` with one plain sentence explaining why you
  need the call, and tell the user which job id waits for approval.
- Only one job runs at a time. Poll `job_status` (every few seconds for
  short jobs, every 30–60 s for builds) and read `job_output` from
  `next_offset`.
- Builds can be silent for a long time. When you report progress, use
  `last_output_at`, `screen_changed_at` and `stuck_suspected` from
  `job_status` ("no log for 12 min, console changed 40 s ago"). Call
  `job_screenshot` only when the user asks to see the VM, then briefly
  describe the screen (installer step, errors, login prompt). If
  `stuck_suspected` is set, tell the user and suggest the live view in the
  gateway terminal; do not cancel the build on your own.
- Every VM you create must be destroyed again before you finish, even after
  failures. Always give disposable VMs an explicit unique `name` (CLI `--name`);
  reuse that name for every lifecycle call, `vm_info`, and the fourth tunnel
  component. Never claim or clean up an existing default VM.
- For a separate host, the binary is pinned by the human: ask them to rebuild
  and restart after code changes. In an explicitly authorized development VM
  you may rebuild and restart locally, passing the actual binary SHA-256.
  Report `binary_sha256` in either case. Never change a remote host's pinned
  binary from an agent workspace.

## Workflow

1. **Preflight.** If the `sailwright-host` tools are missing, run
   `sailwright-gateway connect --check` and report its output; in a container, this usually means the host gateway is stopped or its
   agent directory is not mounted. In an authorized development VM, ensure
   the local gateway is running; discovery uses `~/.sailwright-gateway` with
   no container mount. Then call `gateway_info`. Note host OS/arch, `binary_sha256`,
   `approval_enabled`, available targets and `target_errors` (missing
   hypervisor tools show up there).
2. **Pick targets.** Use `list_targets` (`create` or `start`) and choose
   stable targets for this host, e.g. `ubuntu/server/amd64`.
3. **Run the deterministic scenarios first** from the repository root:

   ```bash
   SAILWRIGHT_E2E_EXECUTOR=gateway go test -tags e2e ./test/e2e \
     -run 'TestScenarios/smoke' -count=1 -v
   SAILWRIGHT_E2E_EXECUTOR=gateway SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64 \
     SAILWRIGHT_NAMED_VM_E2E=1 go test -tags e2e ./test/e2e \
     -run '^TestNamedVMInstances$' -count=1 -timeout 1h -v
   ```

   `local-check` needs an approval on the host; run it only when asked to
   validate local provisioning, and tell the user to watch the gateway
   terminal. If roles need sudo, the user answers with
   `approve-sudo <id>` and types the password there; never ask the user for
   a password yourself. Builds can take hours; run long scenarios in the background
   and keep the user informed.
4. **Explore only when needed.** Use the typed tools (`vm_*`,
   `vm_provision`, `artifact_pull`) for anything the scenarios did not
   cover, or to narrow down a failure. Use `run_cli` with `bundle_root` for
   files from your workspace (absolute paths below `bundle_root`); expect an
   approval prompt. If `bundle_root` is rejected as outside a configured
   agent workspace, don't ask the user to edit `gateway.yaml`. Register it
   with `workspace_register`: write a random hex token to
   `<workspace>/.sailwright-gateway-probe`, pass it as `probe_token` with the
   host path (`$SAILWRIGHT_HOST_WORKSPACE` or ask the user), and have the user
   approve once. No restart or `/mcp` reconnect is needed. Delete the probe
   afterwards.
5. **Clean up.** `list_targets` for `destroy`; every target you touched must
   show `state: missing`. Cancel jobs you started and no longer need.
6. **Report.** Summarise per scenario and step: host OS/arch, binary hash,
   target, pass/fail, duration and the relevant output lines. Reports are in
   `test/e2e/out/<scenario>.md`. Separate facts you observed from guesses.

## Turning findings into scenarios

When you found a check worth repeating, add a YAML scenario under
`test/e2e/scenarios/` instead of only describing it (format in
`docs/testing-workflows.md#writing-scenarios`). Keep it deterministic: fixed
steps, explicit expectations, cleanup in `always`, and a `preflight` check
for any existing state that cleanup could otherwise destroy. Verify it with the fake
CLI self-test (`go test ./test/e2e/...`) and then once against the gateway.
A human reviews and merges it.

## Test Sailwright inside a disposable guest

Use this when testing installers, `sailwright install`, or `provision local`
without modifying the gateway host. If that host is itself a development VM,
check nested KVM and libvirt availability first. The Ubuntu build script already
has a KVM probe and software-emulation fallback; do not introduce a new
compatibility mode solely because CPU flags indicate a virtualized host.

1. Use `gateway_info` and `list_targets` to choose an Ubuntu target. Confirm
   it has no existing VM before taking ownership of cleanup. Reuse a built
   image, or build it through `vm_build` first.
2. Create and start the guest through `vm_create` and `vm_start`; wait for
   each job to finish. Call `vm_info` and save its SSH config in the container.
3. Use `scp -F <config>` with the returned Host alias to copy the appropriate
   guest-OS/architecture Sailwright build to `/tmp/` in the guest. A binary
   being tested inside the guest is distinct from the host's pinned binary.
4. Use `ssh -F <config> <alias>` to run `uname -a`, inspect the guest and run
   the installer, `sailwright install` or `sailwright provision local` there.
   Always make clear that these commands run in the guest. Read exit codes
   and relevant output, then copy test reports back with scp.
5. Destroy the guest through `vm_destroy` in cleanup, including after failures.
   Report the host binary hash, target, guest build and observed results.

The opt-in `TestNamedVMInstances` test creates two named Ubuntu guests, checks
independent SSH and hostnames, destroys one and verifies the other. Run it with
`SAILWRIGHT_NAMED_VM_E2E=1` as documented in `docs/testing-workflows.md`.

The opt-in `TestVMAccess` scenario in `test/e2e` uses the `agent-access` instance
(override with `SAILWRIGHT_E2E_NAME`), verifies SSH and scp, and
performs cleanup. See `docs/agent-gateway.md` for its invocation. Never include
passwords from `vm_info` in reports, shell arguments or committed files.

## Windows Hyper-V test host

When the user authorizes a disposable Windows development VM as gateway host,
run everything natively there: build `sailwright.exe` and
`sailwright-gateway.exe`, start the gateway (with `--bypass-approvals` only if
the user asked to automate approvals on a throwaway host) and run the e2e
tests from PowerShell. Do not set up WSL or Docker just to simulate the
devcontainer: they add another Hyper-V guest and `TestVMAccess` already runs
on Windows (Windows OpenSSH with `SSH_ASKPASS`, no connection sharing). Follow
`docs/agent-gateway.md#testing-on-a-windows-hyper-v-host` for the commands.

- Check `Get-Service vmms`, the Default Switch and free memory before
  creating a VM; the Ubuntu guest takes 8 GB and 4 vCPUs.
- When a guest never reports an IP or Packer waits for SSH, look at the
  Hyper-V Worker-Admin events (18590 crash with the kernel panic text, 18514
  reset, 3108 nested virtualization refused) and a console screenshot before
  changing anything. A guest that boots but crawls on a nested host usually
  points to the outer VM's CPU topology or memory; report that to the user
  instead of working around it in sailwright.
- Background jobs can be stopped by Claude Code when the host runs low on
  memory. Do not restart them on your own; tell the user.

## Develop roles with an Ansible controller in the container

1. Create/start a disposable guest with an explicit `name` as above and call
   `vm_info` with that same name.
2. Write its `ansible_inventory` to an owner-only file outside committed
   content. For SSH it already contains a ProxyCommand. Install `sshpass`
   in the container when using password authentication.
3. For WinRM, start the returned `listen_command` in a container terminal and
   keep it running; the inventory points to its loopback port. Install
   `pywinrm` in the controller container. Never bind the listener to a wildcard.
4. Run `ansible-playbook -i <inventory> <workspace-playbook>` in the container,
   first with `--check`, then normally, then again to check idempotence.
   The controller and workspace roles stay in the container; the managed guest
   is the target. This does not execute those roles on the host.
5. Destroy the guest and remove temporary credential files when finished.
