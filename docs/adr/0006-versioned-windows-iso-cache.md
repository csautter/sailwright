# ADR 0006: Versioned Windows 11 ISO Cache

## Status

Accepted

## Context

Windows 11 builds need Microsoft's installation ISO (about 7 GB per
architecture). Microsoft offers it only through a download page that often
refuses requests from GitHub-hosted runner IPs, and it replaces the media
behind an unchanged page from time to time (for example "v2" refreshes).

Before this decision every build job resolved the ISO itself or read one flat,
unversioned cache object (`win11_25h2_english_<arch>.iso`). Refusals turned
builds red for reasons unrelated to the change under test, and a replaced flat
object could not be told apart from the previous one or rolled back.

The cache layout, the manifest, and the rules for who may contact Microsoft are
shared by the refresh workflow, the cache actions, the build workflows, and the
Go code. They need one written contract.

## Decision

Only the "Refresh Windows 11 ISO" workflow resolves and downloads Windows 11
ISOs from Microsoft for CI. It publishes them to the FTP build cache below
`windows11/iso/`:

- **Objects are immutable.** An ISO is stored as
  `<lower-cased Microsoft file name without .iso>_<YYYYMMDD>.iso`, the date
  being the day it was first seen. An object referenced by a manifest is never
  overwritten. Different bytes under a referenced name fail the refresh; bytes
  identical to a referenced object reuse that object.
- **One manifest per architecture** (`win11-<arch>.json`) points to the current
  object. Schema 1 has the fields `schema`, `arch`, `object`, `source_name`,
  `release`, `sha256`, `size`, `build`, `published_at`, `checked_at`,
  `announced_release` (optional) and `history`. Optional fields may be added
  within schema 1; readers ignore unknown fields. Any change that existing
  readers would misinterpret requires a new schema number, and readers must be
  updated to accept it before any producer writes it.
- **Readers validate** the schema, the object name (`[A-Za-z0-9._-]`, ending
  in `.iso`), and the sha256 format before using a manifest, and verify the ISO
  bytes against the manifest sha256 before use, including files from local
  caches.
- **Publication is atomic.** Each file is uploaded under a temporary name,
  size-checked, and renamed into place. The object is in place before the
  manifest that references it. An unchanged refresh checks the referenced object's
  availability before advancing `checked_at` and restores a missing object from
  verified bytes.
- **Retention** keeps the current object plus two previous ones (`history`).
  Pruning happens only after the new manifest is in place and never deletes the
  current object.
- **Build jobs never contact Microsoft.** They set
  `SAILWRIGHT_WIN11_ISO_OFFLINE=1`, resolve the ISO through the manifest, and
  fail with a pointer to the refresh workflow when it is missing.
- **Blocked is not broken.** `playwright_win11_iso.py` exits with code 3 when
  Microsoft refuses or challenges the request; Go maps that to
  `ErrMicrosoftRefused`, and the refresh records `outcome=refused` without
  failing. A refusal on the hosted runner is retried on the self-hosted Tart
  runner. The run fails only when every runner was refused and the manifest's
  `checked_at` is at least 90 days old.
- **Microsoft is contacted only when due**: no manifest, a forced run, a newly
  announced release (once per announcement), or a last check at least 28 days
  ago.

## Consequences

- Build results no longer depend on whether Microsoft accepts the runner's IP.
- Every published ISO is traceable to a date and a sha256, and the previous two
  versions remain available for rollback by editing the manifest.
- Moving from the flat object requires a one-time adoption run; build workflows
  keep a legacy `blob-name` fallback until it is done
  ([migration steps](../windows-iso-cache.md#migration-from-the-flat-cache)).
- The FTP server must support `RNFR`/`RNTO`. A server that cannot rename onto an
  existing file falls back to delete and rename, with a short window in which
  the manifest is missing.
- When every runner is blocked for 90 days, a person must publish an ISO
  manually ([manual publish](../windows-iso-cache.md#manual-publish)).
