# ADR 0005: Agent Gateway For Host-Side Policy Enforcement

## Status

Proposed (experimental)

## Context

Coding agents run inside devcontainers, but testing Sailwright needs the real
host: hypervisors, KVM, Hyper-V, QEMU and host-run Ansible. Giving an agent a
shell on the host, or mounting the hypervisor sockets into the container, would
hand an untrusted process the full privileges of the operator.

Agents still need to build, create, provision and destroy VMs to run
deterministic E2E scenarios (`test/e2e`), and they need to see results and
console output of long-running builds.

## Decision

Add a separate host-side executable, `sailwright-gateway`, that exposes a fixed
set of Sailwright operations to agents over MCP (Streamable HTTP) and enforces
policy on the host.

- **Separate binary, no CLI internals.** The gateway follows the
  `sailwright-<name>` naming scheme but does not import CLI packages. It
  discovers valid targets at runtime with `sailwright <command> list --json`
  (schema version 1) and refuses other schema versions.
- **Pinned CLI binary.** The operator confirms the SHA-256 of a copied binary at
  startup. Only that copy runs and it is re-hashed before every run. Agents
  cannot supply or swap the binary.
- **Safe set plus console approval.** A strict grammar parses requests. Safe
  operations run with an argv re-rendered by the gateway (no shell, rebuilt
  environment). Everything else, notably anything that executes Ansible content
  from the agent, waits for approval typed on the gateway's own host terminal.
- **Snapshots.** Agent-provided files are used only through `bundle_root`, which
  must resolve inside a configured agent workspace. The gateway copies it into
  a read-only snapshot and runs the snapshot.
- **Trust model.** The agent is untrusted. The bearer token is readable by
  everything in the container, so it is not a boundary against the agent; the
  boundaries are the safe set, the approval channel (host TTY only) and file
  permissions on the agent folder.
- **Transport.** A `0600` Unix socket in a dedicated folder mounted into the
  container (Linux). For Docker Desktop, a loopback TCP listener with token,
  source, `Host` and `Origin` checks. Wildcard binds are refused.
- **Isolated state.** The CLI runs with its own app-data, config and cache
  directories below the gateway data dir. `shared_cache_dir` is an explicit,
  documented opt-out that weakens isolation.
- **Dependency.** Use the official MCP Go SDK
  (`github.com/modelcontextprotocol/go-sdk`) for the protocol, and
  `golang.org/x/term` for TTY handling.

## Consequences

- Agents can run host-level E2E scenarios without a host shell, and the operator
  keeps a single, auditable approval point (`audit.jsonl`).
- Hypervisor VM names are global. An agent's `vm_destroy` affects the same VM
  name as the operator's own use; separate host users or machines are needed
  where that matters.
- Some logic is deliberately duplicated rather than imported, for example the
  VNC snapshot directory layout mirrors `GenerateVirtualMachineSlug`. Changes to
  the CLI layout need a matching gateway change.
- The MCP tool surface is experimental and may change until E2E clients depend
  on it.
- The operator must run the gateway in a foreground terminal and remains the
  security boundary for approvals.
- Only one job runs at a time, and remote (non-local) hosts are not supported.

See [Agent Gateway](../agent-gateway.md) for usage and configuration.
