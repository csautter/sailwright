# Agent Instructions

## Plans

- Write implementation plans as Markdown files to `/workspaces/sailwright-plans`, not into this repository.
- When asked to implement a plan, read it from `/workspaces/sailwright-plans` as well.

## Documentation

- Keep the root `README.md` concise. Put detailed workflows, configuration, and examples in a matching existing subdirectory README or documentation guide, and link to it from the root README.

## Quality Checks & Tooling

- Run and extend release candidate checks with the [release testing runbook](docs/release-testing.md): one approved phase at a time, fresh run directory per rerun, no weakened gates.

- Full host-modifying E2E scenarios are intentionally opt-in. Their absence
  from automatic PR CI is an accepted risk documented in
  [ADR 0007](docs/adr/0007-manual-host-e2e-accepted-risk.md), not a recurring
  review finding. Assess new risks and missing unit or targeted integration
  coverage separately.

- When running `make gosec` inside the devcontainer, if gosec fails with SSA type errors (`export data version 5 is greater than maximum supported version 4`) because the container Go toolchain is newer than `go.mod` (e.g. Go 1.27 vs Go 1.26), run with the toolchain version declared in `go.mod`; `make gosec` prints the exact hint on failure:
  ```bash
  GOTOOLCHAIN=<go.mod version> make gosec
  ```

## Code Review: Intentional CI Coverage

- Read the [E2E CI coverage decision](docs/testing-workflows.md#e2e-ci-coverage-decision) before assessing test execution completeness.
- Full VM lifecycle E2E scenarios remain opt-in. Adding them to routine PR CI would substantially increase CI load and duplicate much of the existing unit, build, deploy, and provisioning coverage. This is an accepted project decision.
- Do not raise a finding, assign High severity, or recommend enabling these scenarios in routine CI solely because CI runs only the tagged E2E smoke scenario. Apply this decision when using review skills and their generic CI-completeness checklists.
- Report a coverage gap only when the reviewed change introduces a concrete risk that existing automated tests do not cover. Identify the missing behavior and propose the smallest targeted check. Revisit broad lifecycle CI execution only when the user explicitly requests reconsidering this tradeoff.

## Accepted Cache Migration Tradeoff

- During the versioned dependency-cache migration, cross-version builds sharing a cache may prune each other's dependency files. This limitation is accepted; do not require cache locks, leases, or a separate garbage-collection design to complete this migration. See the [local-cache migration tradeoff](docs/windows-iso-cache.md#local-cache-migration-tradeoff).
