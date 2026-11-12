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
- **Workspace registration (amendment).** `gateway.yaml` stays read-only
  through MCP. Additional workspaces live in `agent_workspaces.json` in the
  state directory. The agent can add one with `workspace_register` only after
  the gateway finds an agent-written probe token in the host directory, which
  proves the agent can write there (its mount or a directory inside it). The
  operator must then approve it on the host terminal, and the path must still
  resolve to the approved directory at that point. A registered host path is
  stored resolved and pinned: it is refused on use once it is no longer the
  same real directory, because the agent can replace a directory inside its
  mount with a symlink. This removes the YAML edit and restart for each new
  devcontainer without letting the agent name arbitrary host directories.
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
  tunnel at another address on ports 22, 5985 or 5986. The tunnel amendment
  below limits this to the host's directly connected subnets; the remainder is
  accepted: the gateway's purpose is to keep the agent off the host OS, and the
  agent's container normally has direct network access.
- Shared mode lets the agent replace the operator's cached images with
  `vm_build` and `artifact_pull`. This is accepted and they stay in the safe
  set: builds run only the templates embedded in the pinned binary and pulls
  only allowlisted references, so the agent cannot put content of its own into
  an image. It can only replace an image with another build or allowlisted
  artifact, such as an older tag. Operators who need their cache untouched
  use `state_isolation: isolated`.

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
fails closed into an approval job. The job runs that check itself, right
before its command and cancellable with the job, not the tool call: on Hyper-V
the query can take minutes, longer than MCP clients wait, and checking last
narrows the gap between check and use. A different id drops the record, so the VM
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

## Amendment: revoking missing named VMs (2026-11-07)

Named VMs disappear from provider listings when removed. After refreshing the
create listing, the gateway explicitly queries known but unlisted named targets
with `vm identity --name`. Confirmed absence revokes ownership and grants,
including on UTM/Tart where instance IDs are unavailable. Failed identity
queries preserve records; a partial listing alone never proves absence, and
records newer than the listing are not pruned.

## Amendment: tunnel destinations and guest password redaction (2026-11-02)

Tunnels only connect to guest addresses inside a subnet of one of the host's
own interfaces, plus the operator's `tunnel_allowed_cidrs` for routed guest
networks. This covers NAT bridges and switches (libvirt, Hyper-V, UTM, Tart)
and bridged guests on the LAN, and removes routed destinations, such as the
internet and other VLANs, that a guest reporting a forged address could
otherwise reach. On Hyper-V, where the CLI accepts any reported address when
the host has no address on the switch, such a guest is now unreachable through
a tunnel unless its network is listed. A tunnel's port must also match the
guest protocol (22 for SSH, 5985 and 5986 for WinRM).

Guest passwords stay in the gateway's redactor for the whole run, at any
length, except a password equal to the guest username. Such a password is not
secret and would otherwise turn common words such as `vagrant` or `packer`
into the redaction marker in all later output.

The on-link rule trusts every subnet the host has an interface in, including
VPN and corporate adapters with wide prefixes. A guest that forges its address
can reach ports 22, 5985 and 5986 of hosts there while such an adapter is
connected. This is accepted for the same reason as bridged LAN guests and is
documented for operators.

## Amendment: macOS instance identity and UTM registration safety (2026-11-07)

This amendment supersedes the statement in the VM instance identity amendment
that Sailwright reads no UTM or Tart instance identifier. The ADR remains
accepted (experimental).

UTM identity is the managed bundle's `Information.UUID`. Tart identity is a
SHA-256 hash of the local VM directory's filesystem device, inode and birth
time, using `TART_HOME` and `TART_MACOS_VM_NAME` when set. Directory lookups reject
paths escaping Tart's home, including symlinks. Tart clones can share hardware
identifiers, so those identifiers do not prove local instance ownership.
Both identity queries work offline, without starting or registering a VM.
The existing gateway ownership and approval checks compare these identities
just as they compare libvirt and Hyper-V identifiers.

A copied UTM bundle can retain its UUID. Lifecycle operations therefore verify
that the selected registered UUID points to the managed bundle before checking
status, starting, stopping or deleting it. Matching UUIDs do not exempt a
registration from path verification. Aliases resolving to the same existing
bundle are accepted. Start preserves replacement bundles during stale-entry
cleanup; create remains offline and defers registration repair until start.

UTM's scripting collection excludes entries with bundles it could not load.
Before registration cleanup, Sailwright also reads persistent registry entries.
If a matching entry is unavailable through scripting, start and destroy fail
with instructions to remove the unavailable entry manually in UTM's library.
Destroy listing also reports this error when the bundle is absent. Preferences
can lag a deletion; a stale snapshot can require retrying. Sailwright does not
edit UTM preferences to remove entries. Scriptable entries are stopped and
deleted through UTM before remaining bundle files are removed.

Legacy ownership records without an instance ID retain the existing first-ID
adoption behavior: an upgrade cannot reconstruct which instance an old record
originally described. Operators must clear those records and restart the
gateway before replacing guests outside it. Native UTM/Tart lifecycle and
approval validation remains tracked in issue #302 and opt-in under the manual
host E2E decision in ADR 0007.

## Amendment: ownership gate in isolated mode (2026-11-11)

Isolated state separates app-data, Vagrant state and caches, but not hypervisor
VM names: libvirt (`qemu:///system`), Hyper-V, UTM and Tart find VMs by name, so
an isolated gateway's `vm_info`, tunnels and lifecycle jobs reach the
operator's VMs. The ownership gate (approval for VMs the gateway did not
create) therefore applies in both modes; this supersedes "In shared mode" in
the shared CLI data amendment's consequences.

Consequences:

- Isolated gateways from before this amendment kept no ownership records, so
  their existing VMs need one approval per gateway run until they are destroyed
  and recreated through the gateway. Without an approval console the agent
  cannot use or destroy them. They are not adopted automatically: with global
  names, adoption cannot tell them from the operator's VMs.
- A one-time startup note tells operators of isolated gateways without
  ownership records about this.
