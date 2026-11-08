# ADR 0001: Destroy Implementation Contract For VM Configs

## Status

Accepted

## Context

`alchemy create` materializes host-local VM state:

- UTM bundles on macOS
- Tart local clones on macOS
- Hyper-V Vagrant machines and registered boxes on Windows

Without a matching `destroy` path, the repository accumulates one-way workflows and stale host resources. That becomes a maintenance problem for both humans and coding agents, especially when new `VirtualMachineConfig` entries are added in [pkg/build/virtual-machine.go](/workspaces/dev-alchemy/pkg/build/virtual-machine.go).

## Decision

Every VM config that can be created by the CLI must also have a destroy implementation before it is merged.

Destroy implementations must be:

- Deterministic: derive the resource identity directly from `VirtualMachineConfig` and documented environment overrides.
- Idempotent: return success when the VM is already absent.
- Host-local: remove only the resources created by `alchemy create`; do not delete build artifacts from the managed app-data cache.
- Routed through the shared dispatcher in [pkg/deploy/destroy.go](/workspaces/dev-alchemy/pkg/deploy/destroy.go).

All UTM lifecycle operations must resolve `SAILWRIGHT_UTM_VM_DIR` consistently.
Create, state inspection, start/stop, provisioning and destroy share the bundle
path resolver. An unset or empty override retains the existing UTM directory;
changing the override selects another location and does not migrate VMs.

## Required Changes When Adding A VM Config

When adding or changing a VM definition in [pkg/build/virtual-machine.go](/workspaces/dev-alchemy/pkg/build/virtual-machine.go), an agent must complete all of the following in the same change:

1. Define the destroy target identity.
   The implementation must be able to compute the local VM name, bundle path, or Vagrant identity from `VirtualMachineConfig`.
2. Implement the host-specific destroy routine in `pkg/deploy/`.
   Follow the existing naming pattern: `Run<Engine/Platform>Destroy...`.
3. Wire the config into [pkg/deploy/destroy.go](/workspaces/dev-alchemy/pkg/deploy/destroy.go).
   `SupportsDestroy` and `RunDestroy` must recognize the new config.
4. Expose the config through [cmd/cmd/destroy.go](/workspaces/dev-alchemy/cmd/cmd/destroy.go).
   If `create` can select it, `destroy` must be able to select the same tuple.
5. Add or update tests.
   At minimum, keep the invariant in [cmd/cmd/destroy_test.go](/workspaces/dev-alchemy/cmd/cmd/destroy_test.go) passing: every create-supported config must also support destroy.
6. Document any destroy-specific environment variables or cleanup semantics.
   If destroy behavior depends on host tools, naming overrides, or provider-specific state, record that in the relevant README or ADR update.

## Implementation Rules

- `destroy` must be safe to run multiple times.
- `destroy all` must iterate only destroy-supported configs for the current host OS.
- Unsupported engines must fail with an explicit `not implemented` error.
- Future reviewers should reject any VM config addition that updates `create` support without a corresponding destroy path.

## Consequences

- New VM config work is slightly larger, but the lifecycle stays symmetric.
- Agents have a concrete checklist and code touchpoints instead of inferring destroy behavior.
- The repository gains a durable guardrail: create support is not considered complete without destroy support.
