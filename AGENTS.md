# Agent Instructions

## Plans

- Write implementation plans as Markdown files to `/workspaces/sailwright-plans`, not into this repository.
- When asked to implement a plan, read it from `/workspaces/sailwright-plans` as well.

## Quality Checks & Tooling

- When running `make gosec` inside the devcontainer, if gosec fails with SSA type errors (`export data version 5 is greater than maximum supported version 4`) because the container Go toolchain is newer than `go.mod` (e.g. Go 1.27 vs Go 1.26), run with the toolchain version declared in `go.mod`; `make gosec` prints the exact hint on failure:
  ```bash
  GOTOOLCHAIN=<go.mod version> make gosec
  ```
