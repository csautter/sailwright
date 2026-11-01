# ADR 0005: Agent Gateway For Host-Side Policy Enforcement

## Status

Accepted (experimental)

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
  `sailwright-<name>` naming scheme but does not import CLI packages (tests
  may import them to detect drift). It
  discovers valid targets at runtime with `sailwright <command> list --json`
  (schema version 1) and refuses other schema versions.
- **Pinned CLI binary.** The operator confirms the SHA-256 of a copied binary at
  startup. Only that copy runs and it is re-hashed before every run. Agents
  cannot supply or swap the binary.
- **Safe set plus console approval.** A strict grammar parses requests. Safe
  operations run with an argv re-rendered by the gateway (no shell, rebuilt
  environment). Everything else, notably anything that executes Ansible content
  from the agent, waits for approval typed on the gateway's own host terminal.
  Catalog role selection (`--role`) and alternate source configurations
  (`--config`) execute Ansible content and therefore require that approval.
- **Snapshots.** Agent-provided files are used only through `bundle_root`, which
  must resolve inside a configured agent workspace. The gateway copies it into
  a read-only snapshot and runs the snapshot.
- **Trust model.** The agent is untrusted. The bearer token is readable by
  everything in the container, so it is not a boundary against the agent; the
  boundaries are the safe set, the approval channel (host TTY only) and file
  permissions on the agent folder.
- **Transport.** A `0600` Unix socket in a dedicated folder mounted into the
  container (Linux). For Docker Desktop, a loopback TCP listener with token,
  source, `Host` and `Origin` checks. Wildcard binds are refused. The TCP
  listener is off by default on Linux (`listen: off`) and required on Windows.
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
  the CLI layout need a matching gateway change; `TestVncSnapshotDirMirrorsCLISlug`
  enforces this.
- The MCP tool surface is experimental and may change until E2E clients depend
  on it.
- The operator must run the gateway in a foreground terminal and remains the
  security boundary for approvals.
- Only one job runs at a time, and remote (non-local) hosts are not supported.

See [Agent Gateway](../agent-gateway.md) for usage and configuration.

## Amendment: shared CLI data (2026-10-31)

The original isolated-state decision is amended. App-data and caches are shared
with the operator's CLI by default for new gateways, with `state_isolation:
isolated` available for the previous layout. When the setting is unset and
`data_dir/app` from an older gateway exists, isolated mode is kept so upgrades
never orphan managed VMs. Config remains gateway-owned because role sources
execute Ansible code on the host. Embedded assets from the pinned binary are
extracted into a separate gateway directory to avoid cross-version replacement.

The CLI coordinates operations with OS file locks in app-data. Target operations
use the same lock regardless of command; whole-set operations and registry
transfers exclude target operations. Startup checks a versioned state-schema and
locking contract, warns about compatible CLI version differences, and refuses
unsafe sharing. Gateway cleanup never deletes shared caches. Existing isolated
state is preserved and accompanied by a one-time migration notice.

Consequences of the amendment:

- In shared mode builds and pulls replace cached images the operator also uses.
  VM instances are gated by ownership: VMs the gateway created (a successful
  `create` job, since `create` refuses existing VMs) are open to the agent;
  for any other VM, lifecycle jobs need approval, tunnels are refused and
  `vm_info` raises an asynchronous access request whose approval lasts for the
  gateway run. Ownership is dropped on destroy or when `create list` reports the
  VM gone. See the instance identity amendment for VMs recreated between
  listings and for failed creates.
- "Only the pinned copy runs" has one exception: at startup the gateway runs
  `sailwright --gateway-metadata` from PATH, without passthrough secrets, to
  compare state contracts. An installed CLI without the contract is a warning,
  not a refusal; an incompatible reported contract is refused in shared mode.
- The shared app-data layout is mirrored from `pkg/build` and enforced by
  `TestSharedAppDataMirrorsCLIDefaults`.
- Tunnel destinations come only from the pinned CLI, but some engines learn
  them from the guest. A guest under agent control can therefore point the
  tunnel at another LAN address on ports 22, 5985 or 5986. This is accepted:
  the gateway's purpose is to keep the agent off the host OS, and the agent's
  container normally has direct network access. On Hyper-V, where the guest
  reports all addresses, the host-side switch and subnet decide which address
  is used.

## Amendment: lock protocol version 2 (2026-11-01)

The Windows lock byte moves from offset zero to offset 2^62 so other processes
can read the lock holder metadata. This changes the cross-process locking
contract even though the app-data schema is unchanged. The CLI and gateway now
advertise and require lock protocol 2 for shared state. A CLI reporting protocol
1 must be updated together with the pinned CLI before sharing state.

## Amendment: conservative network reporting (2026-11-01)

Suppressing the tunnel LAN warning requires a known NAT or user network. Libvirt
mode is determined from active domain and network XML, rather than the connection
URI or network name. Failed queries and ambiguous modes remain unknown. Tart
also reports unknown because the querying process environment cannot establish
the network mode of an already running guest. Bridged and unknown modes continue
to allow tunnels with the audit warning.

## Amendment: VM instance identity and failed creates (2026-11-01)

Ownership keyed only by target name could not tell a VM the operator destroyed
and recreated between two listings from the one the gateway created. The CLI now
reports the hypervisor instance id (libvirt domain UUID, Hyper-V VM Id) in
`vm info --json` and in a new `vm identity` command that also answers for stopped
VMs. The gateway stores the id with each ownership record and grant. `vm_info`
and tunnels compare it with the id of their fresh lookup; lifecycle jobs on a
VM whose record has an id run `vm identity` first, and a failed lookup there
fails closed into an approval job. A different id drops the record, so the VM
counts as the operator's. A record made without an id (the lookup after create
failed) adopts the first id it sees. UTM and Tart expose no id that sailwright
reads, so the listing-based gap remains on macOS hosts.

A failed `create` used to leave a VM that was neither the gateway's nor
deletable without approval. The gateway now passes each create job a private
marker path in `SAILWRIGHT_CREATE_STARTED_FILE`. The CLI writes it after it has
checked, while holding the target's operation lock, that no VM exists, and
before deployment starts; it aborts the create if the marker cannot be written.
A create job that fails after writing the marker records ownership when the VM
exists afterwards. A create refused for an existing VM writes no marker and
claims nothing. A pinned CLI without these features behaves as before.
