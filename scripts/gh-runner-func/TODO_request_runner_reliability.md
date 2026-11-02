# TODO: request_runner Reliability Hardening

This file documents **non-breaking** follow-up work for intermittent `500 Internal Server Error` responses from `request_runner` in `function_app.py`.

Scope: `scripts/gh-runner-func/function_app.py`
Route: `request_runner`

## Why this exists
Current behavior has a few error paths that can fail intermittently (network blips, secret retrieval timeouts, Azure resource-name collisions) and return generic `500` responses.

## TODO 1: Wrap external calls with explicit exception handling
Problem:
- `kv_client.get_secret("github-runner-pat")` and `requests.post(...)` can raise exceptions outside protective `try/except`.
- Result: random/generic 500 from unhandled exceptions.

Instructions:
1. Add `try/except` around Key Vault PAT retrieval.
2. Add `try/except requests.RequestException` around GitHub registration-token request.
3. Return clear error body and status code mapping:
   - Key Vault unavailable/timeout -> `502` or `503`
   - GitHub timeout/network -> `502` or `504`
4. Keep sensitive values out of logs.

Suggested target locations:
- Around current lines where PAT is read and GitHub POST happens.

## TODO 2: Prevent resource-name collisions under concurrency
Problem:
- VM and network resources use mostly static names (`VM_NAME`, `IP_NAME`, `NIC_NAME`, etc.).
- Concurrent calls can fail with conflicts/"in use"/already exists.

Instructions:
1. Generate per-request unique suffix (timestamp or short UUID).
2. Build unique names for VM/NIC/IP/NSG (or isolate each request in dedicated resource group).
3. Keep Azure name-length and character constraints in mind.
4. Include generated names in structured logs and response payload.

Suggested target locations:
- Network resource creation and `begin_create_or_update(..., os.environ["VM_NAME"], ...)` calls.

## TODO 3: Improve upstream error propagation
Problem:
- Non-201 from GitHub registration endpoint is always mapped to generic `500`.

Instructions:
1. Parse GitHub error response safely.
2. Log compact diagnostic details (status + short reason).
3. Return more informative status/body to caller (do not leak PAT or secrets).

Examples:
- `401/403` from GitHub -> `502` with message "GitHub authentication/authorization failed"
- `404` repo not found -> `400` (if client input issue)
- `429` rate limit -> `503` with retry hint

## TODO 4: Validate environment configuration at startup or first call
Problem:
- Missing env vars can trigger `KeyError` and become generic 500.

Instructions:
1. Define required env var list (e.g., `VAULT_URL`, `SUBSCRIPTION_ID`, `LOCATION`, `VM_NAME`, `ADMIN_USERNAME`, `VM_SIZE`, `CUSTOM_IMAGE_ID`).
2. Validate once and cache result.
3. If invalid, return deterministic error with list of missing keys (safe to expose key names).

## TODO 5: Improve observability for triage
Problem:
- Current logs make it harder to correlate intermittent failures.

Instructions:
1. Add request correlation ID (from header if present, otherwise generate).
2. Add structured logs at each stage:
   - auth check complete
   - input validated
   - PAT retrieved
   - GitHub token acquired
   - network resources created
   - VM create submitted
3. Include stage-specific exception class and message.

## TODO 6: Add resilience controls
Instructions:
1. Add retry with backoff for transient Key Vault and GitHub failures (bounded attempts).
2. Use idempotency strategy for repeated client calls (optional but recommended).
3. Apply reasonable timeouts on all external SDK operations.

## TODO 7: Add tests for failure paths
Minimum test matrix:
1. Key Vault PAT retrieval throws -> expected non-500 generic fallback behavior.
2. GitHub POST timeout -> mapped status/message.
3. GitHub 4xx/5xx -> mapped status/message.
4. Name collision/concurrency simulation -> no silent generic 500.
5. Missing env var -> deterministic config error.

## TODO 8: Detect VM provisioning failures after the create request is accepted
Problem:
- `compute.virtual_machines.begin_create_or_update(...)` returns a poller that is never awaited.
- Only errors on the initial PUT reach the spot fallback or the `500` path. Failures during the long-running operation (for example `OverconstrainedAllocationRequest`, or a spot VM evicted while provisioning) still return `202`, and the caller waits for a runner that never registers.

Instructions:
1. Keep returning `202` quickly: the Consumption plan's HTTP timeout rules out blocking on `.result()` for a full Windows VM provisioning.
2. Either poll the spot attempt briefly (bounded `poller.wait(timeout=...)`) so early allocation failures still fall back to a regular VM, or expose VM provisioning state through a status endpoint, like `delete_resource_group` does for deletion.
3. Log the ARM error code (`HttpResponseError.error.code`) when provisioning fails.

## Acceptance Criteria
1. No unhandled exceptions in `request_runner` happy-path dependencies.
2. Intermittent external failures produce deterministic, diagnosable responses.
3. Concurrent calls do not fail due to static resource naming collisions.
4. Logs are sufficient to identify failing stage within one request.

## Notes for coding assistants
- Keep changes backward-compatible for API shape unless explicitly agreed.
- Prioritize small, reviewable commits per TODO section.
- Avoid leaking secrets in logs or responses.
- Update this file by checking off completed TODO items.
