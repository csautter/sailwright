# ADR 0007: Manual Host E2E Testing as an Accepted Risk

## Status

Accepted (2026-11-06)

## Context

Full Sailwright E2E scenarios build or pull VM images, create and boot guests,
provision them, check idempotency, and destroy them. They require suitable
hypervisors, substantial runtime and storage, and permission to modify the
host. Running every platform and target automatically on every pull request
has a significant CI cost and operational burden.

## Decision

Full host-modifying E2E scenarios remain opt-in, run manually on suitable hosts
or disposable runners. They are not required in automatic pull-request CI.
This includes the VM lifecycle matrices, target-specific lifecycle and destroy
scenarios, and local provisioning checks in `test/e2e/scenarios`.

Automatic CI continues to run unit tests, E2E runner tests, the CLI smoke
scenario, and the existing targeted integration and platform workflows.
The recorded quickstart workflow remains manually dispatched.

## Accepted risk

Automatic CI can pass despite regressions in real VM lifecycle transitions,
host integration, or provisioning idempotency. The project explicitly accepts
this residual risk in exchange for bounded CI cost and host requirements.
Manual validation should be selected for changes affecting these behaviors;
commands and prerequisites are documented in
[Testing Workflows](../testing-workflows.md#deterministic-e2e-scenarios).

## Review policy

The absence of automatic full host E2E execution is an accepted tradeoff, not
an actionable review finding or merge blocker by itself. Reviews should cite
this ADR when assessing CI completeness instead of repeatedly raising the
same coverage gap.

This does not exempt new logic from appropriate unit or targeted integration
tests, excuse regressions in existing CI, or accept newly introduced risks
beyond this scope. Revisit this decision when host testing requirements,
available CI capacity, or evidence from escaped regressions changes.
