# Agent Instructions

## Plans

- Write implementation plans as Markdown files to `/workspaces/sailwright-plans`, not into this repository.
- When asked to implement a plan, read it from `/workspaces/sailwright-plans` as well.

## Documentation

- Keep the root `README.md` concise. Put detailed workflows, configuration, and examples in a matching existing subdirectory README or documentation guide, and link to it from the root README.

## Quality Checks & Tooling

- Full host-modifying E2E scenarios are intentionally opt-in. Their absence
  from automatic PR CI is an accepted risk documented in
  [ADR 0007](docs/adr/0007-manual-host-e2e-accepted-risk.md), not a recurring
  review finding. Assess new risks and missing unit or targeted integration
  coverage separately.

- When running `make gosec` inside the devcontainer, if gosec fails with SSA type errors (`export data version 5 is greater than maximum supported version 4`) because the container Go toolchain is newer than `go.mod` (e.g. Go 1.27 vs Go 1.26), run with the toolchain version declared in `go.mod`; `make gosec` prints the exact hint on failure:
  ```bash
  GOTOOLCHAIN=<go.mod version> make gosec
  ```
