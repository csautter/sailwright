# Dependency alert triage (2026-11-06)

Baseline: `origin/main` at `8c4cfd5` (merged PR #277). All ten open PRs
were checked, including their changed files; none updated `go.mod` or
`go.sum`, and no open Renovate/security PR duplicated these updates.

## Findings and changes

| Code scanning alerts | Advisory | Assessment and action |
| --- | --- | --- |
| #152 | [GO-2026-6865](https://vuln.go.dev/ID/GO-2026-6865.json) | Reachable zstd decompression through `pkg/build.downloadWebFileDependencyFromSource` → go-getter → compress/zstd → huff0/fse. Upgrade compress v1.20.1 → v1.20.2, the first fixed release. |
| #141–145 | [GO-2026-6603](https://vuln.go.dev/ID/GO-2026-6603.json), [6610](https://vuln.go.dev/ID/GO-2026-6610.json), [6611](https://vuln.go.dev/ID/GO-2026-6611.json), [6612](https://vuln.go.dev/ID/GO-2026-6612.json), [6617](https://vuln.go.dev/ID/GO-2026-6617.json) | All specify x/net v0.60.0 as fixed. Upgrade v0.59.0 → v0.62.0 to also cover the HTML advisories below. |
| #146–147 | [GO-2026-6860](https://vuln.go.dev/ID/GO-2026-6860.json), [6866](https://vuln.go.dev/ID/GO-2026-6866.json) | x/net/html is absent from the production package dependency list. Both are nevertheless covered by v0.62.0, their first fixed release. |
| #140 | [GO-2026-5932](https://vuln.go.dev/ID/GO-2026-5932.json) | OpenPGP packages are absent from `go list -deps ./...`; `go mod why golang.org/x/crypto/openpgp` reports they are not needed. Remains a module-level finding; see constraints below. |

The [compress maintainer advisory](https://github.com/klauspost/compress/security/advisories/GHSA-fpmh-frq8-7rj8)
describes a panic from a crafted two-block zstd frame. The new local HTTP
download regression test reproduces that panic with v1.20.1, exercising the
actual go-getter streaming decoder. With v1.20.2 it requires an error and
removal of the partial destination, and also verifies a valid compressed
download. The fixture and its upstream license are in `pkg/build/testdata`.
Compress becomes a direct requirement because the valid-input test uses its
encoder.

HTTP/2 package reachability is real: `go mod why golang.org/x/net/http2`
shows go-getter → cloud storage → Google API HTTP transport. That transport
calls `http2.ConfigureTransports`; gRPC also uses HTTP/2 framing. The existing
main-branch scan reports reachable symbols for all five HTTP/2 advisories.
Symbol reachability does not establish every exploit precondition:

- GO-2026-6611 applies to clients and servers; outbound HTTP/2 connections
  make this directly relevant.
- GO-2026-6610 concerns malformed responses and response smuggling when
  forwarding to HTTP/1 clients. No such forwarding path was established here.
- GO-2026-6603, 6612 and 6617 describe server behavior. The gateway uses
  `http.Server.Serve` on plain listeners, with no TLS/h2c configuration found;
  its current listeners do not establish an exposed HTTP/2 server path.

These distinctions do not justify dismissing reachable dependency alerts.
Updating the shared dependency covers the vulnerable implementations.

## Compatibility and remaining constraints

- compress v1.20.2 requires Go 1.25; x/net v0.62.0 requires Go 1.26.0.
  The repository's Go 1.26.0 minimum and go1.27.3 toolchain remain unchanged.
- x/net requires x/crypto v0.58.0 and x/text v0.43.0, so these are necessary
  transitive updates. No other module versions change.
- GO-2026-5932 lists removal of OpenPGP from x/crypto at
  `v0.58.1-0.20261009181718-2f1f834330b2`. At triage time, `go list -m
  golang.org/x/crypto@latest` returns v0.58.0. A tagged release containing
  the removal is unavailable. Keep the unused-package module alert open;
  do not force a prerelease merely to remove a scan finding. Revisit after
  a compatible tagged release or if OpenPGP enters the package graph.
- The Dependabot alert API returns HTTP 403 and says alerts are disabled.
  This triage uses the accessible code scanning alerts and authoritative Go
  vulnerability records; it cannot claim a separate Dependabot inventory.
- Alert #136 (gosec file permissions) is outside this dependency update.
- Local validation is Linux amd64 with Go 1.27.3. Native macOS/Windows and
  VM workflows require separate validation; no VMs were modified.
- The first unrestricted baseline govulncheck run was terminated after host
  memory exhaustion stalled analysis. Its incomplete result is not a clean
  security assessment. Green SARIF-producing jobs likewise do not establish
  that alerts are resolved. Leave alerts undismissed and let the main-branch
  rescan verify remediation after merge.

## Validation

On Linux amd64, Go 1.27.3:

- `go test ./pkg/build -run '^TestDownloadWebFileDependency' -count=1`
  passes, including valid zstd, the panic regression, checksum rejection,
  retry, fallback and download progress behavior.
- `go test ./pkg/oci ./internal/gateway/... -count=1` passes.
- `go vet ./pkg/build ./pkg/oci ./internal/gateway/...` passes.
- `go mod verify` and `git diff --check` pass.
- govulncheck v1.9.0, source symbol analysis over `./...`, completes with
  only GO-2026-5932 at module level, with no reachable vulnerable symbols.
  All eight compress/x/net advisories above are absent from its findings.

The completed checks used `GOMAXPROCS=2`, Go command `-p 2` where supported,
and `GOMEMLIMIT=768MiB` (1200MiB plus `GOGC=50` for govulncheck) to keep
memory usage bounded on the shared host.
