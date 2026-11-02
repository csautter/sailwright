# ADR 0007: CLI Self-Update and Update Notices

## Status

Accepted

## Context

Script and manual installations used to update only by rerunning the install
script. Users had no way to see the installed version or learn that a newer
release existed. Adding `sailwright update` means the CLI replaces its own
executable and the adjacent gateway. It also means the CLI may contact GitHub
outside an explicit command. Both are security-relevant and hard to change
once users depend on them, so the trust model and the boundaries need one
written contract.

## Decision

- **Explicit updates only.** `sailwright update` is the only path that
  installs anything. There is no automatic installation, daemon, or
  background service. Homebrew, Scoop, winget and development builds are
  refused and pointed to their own update mechanism.
- **Trust model.** Assets, names and the checksum manifest are the same ones
  the install scripts use.
  - Every archive must match its SHA-256 entry in the manifest.
  - When cosign is installed, the manifest signature must verify against the
    pinned identity
    `https://github.com/csautter/sailwright/.github/workflows/release-binaries.yml@refs/heads/main`
    and the GitHub Actions OIDC issuer. A failure aborts the update.
  - Without cosign, only checksums are checked. That detects corrupted
    downloads but not a compromised release. The updater warns about it, and
    the documentation recommends requiring signatures.
  - `--require-signature` or `"require_signature": true` in `update.json`
    makes the signature mandatory. Invalid settings stop updates rather than
    silently dropping this policy.
- **Transport.** Only HTTPS is used, and redirects to plain HTTP are
  rejected. `SAILWRIGHT_UPDATE_BASE_URL` exists only for local fixtures and
  accepts loopback addresses only. Signature identity stays pinned even when
  the override is set.
- **Replacement.** All binaries are verified and staged in the installation
  directory before any of them is replaced.
  - Unix keeps a hard-link backup, or a copy when the filesystem has no hard
    links, and renames each new binary into place atomically.
  - Windows renames the running executables to `.old` and removes them on a
    later invocation.
  - Any failure rolls back completed renames.
  - A persistent `.update-lock` file guarded by an OS lock serializes
    updaters.
  - The updater never elevates privileges.
- **Downgrades** require an explicit `--version` and an interactive
  confirmation, even with `--yes`.
- **Update notices are opt-in and off by default.** Checks are skipped
  without a TTY on stderr, when `CI` is set, for development builds, and for
  the commands `update`, `version`, `help` and completion.
  - When enabled, an eligible invocation sends at most one GET to
    `/releases/latest` per interval (minimum 15 minutes). It carries only a
    `User-Agent: sailwright/<version>` header and reads only the redirect.
  - A completed check starts the interval, even a failed one. A check that
    never completed is retried after five minutes.
  - Notices go to stderr only, at most once per interval.
  - `SAILWRIGHT_NO_UPDATE_CHECK=1` overrides every way of enabling checks.
- **Agent gateway.** `sailwright update` is outside the gateway safe set and
  needs host approval like any other command (ADR 0005). Its risk hint is
  `HOST-MODIFYING` except for `update --check`, which only reads the latest
  release tag.

## Consequences

- Users on script or manual installs can update with a single command and
  check for updates from scripts (`update --check` exits 2 when an update is
  available).
- Releases must keep the asset naming, the checksum manifest, the cosign
  signature and the signing workflow identity stable. Changing any of them
  breaks both the updater and the install scripts, so both must be changed
  together.
- By default, authenticity depends on cosign being installed. Verifying the
  signature in-process (for example with `sigstore-go`) would remove that
  dependency at the cost of a large dependency tree. That can be revisited by
  superseding this decision.
- Releases older than the first one with `update` cannot update themselves.
  Users who downgrade below it must rerun the install script.
