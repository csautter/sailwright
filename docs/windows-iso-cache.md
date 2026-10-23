# Windows 11 ISO cache

CI never downloads Windows 11 from Microsoft inside a build job. Exactly one workflow,
[Refresh Windows 11 ISO](../.github/workflows/refresh-windows-iso.yml), talks to Microsoft, and only
when a check is worthwhile. It publishes ISOs under versioned names to the FTP build cache and keeps
a manifest that points to the current one. Every other job resolves the ISO through that manifest.

Microsoft often rejects Windows 11 ISO requests from GitHub-hosted runner IPs (error code
`715-123130`). That is a *blocked* result, not a code defect, and is never a red build outside the
staleness guard below.

## Layout (FTP base `/private`)

```
windows11/iso/win11_25h2_english_x64_20261023.iso      # immutable, never overwritten
windows11/iso/win11_25h2_english_x64v2_20270114.iso    # Microsoft media refresh ("v2")
windows11/iso/win11_25h2_english_arm64_20261023.iso
windows11/iso/win11-amd64.json                         # pointer, overwritten on change
windows11/iso/win11-arm64.json
```

**Object name:** the base name of Microsoft's download URL (`Win11_25H2_English_x64v2.iso`),
lower-cased, plus `_<YYYYMMDD>` for the day the ISO was first seen. Names that do not match
`^Win11_\d{2}H[12]_[A-Za-z]+_(x64|Arm64)(v\d+)?\.iso$` become
`win11_unknown_english_<arch>_<date>.iso` and log a warning.

**Manifest** (`win11-<arch>.json`, schema 1):

```json
{
  "schema": 1,
  "arch": "amd64",
  "object": "win11_25h2_english_x64v2_20270114.iso",
  "source_name": "Win11_25H2_English_x64v2.iso",
  "release": "25H2",
  "sha256": "…",
  "size": 7814184960,
  "build": "",
  "published_at": "2027-01-14T04:12:00Z",
  "checked_at": "2027-02-11T04:03:00Z",
  "history": [{"object": "…", "sha256": "…", "published_at": "…"}]
}
```

`build` is optional and currently empty. The current object plus the two previous ones are kept
(`history`); older objects are deleted after the new manifest is uploaded. The object the manifest
points to is never deleted. The ISO is uploaded before the manifest, so readers switch atomically.

## How build jobs use it

Build workflows list the ISO as
`{"local-path":"./.dev-alchemy/cache/windows11/iso/win11_english_<arch>.iso","manifest":"windows11/iso/win11-<arch>.json"}`.
The `download-build-cache` action fetches the manifest, downloads `windows11/iso/<object>`, verifies
the sha256 (deleting the file on mismatch) and writes the manifest next to the ISO as
`<local-path>.json`. The Tart runner's local cache is keyed by the versioned object name, so a new
ISO never hits a stale copy. `upload-build-cache` skips these entries ("managed by refresh
workflow"). The manifest feature is FTP only; the legacy Azure backend fails with a clear message.

Build jobs set `DEV_ALCHEMY_WIN11_ISO_OFFLINE=1`. If the ISO is missing, the build fails
immediately with "Windows 11 ISO missing at …; Microsoft download disabled in CI" instead of
falling back to Playwright.

Locally the ISO lives at `win11_english_<arch>.iso`. A legacy `win11_25h2_english_<arch>.iso` is
renamed once.

## When Microsoft is contacted ("due" rule)

The workflow runs every Monday and evaluates the rule offline first:

1. No manifest: due.
2. `workflow_dispatch` with `force: true`: due.
3. The release notes page lists a newer `Version NNH[12]` than `manifest.release`: due. A failed
   lookup is ignored and the age rule applies.
4. `now - checked_at >= 28 days`: due (catches `v2` media refreshes).
5. Otherwise skipped; the reason is written to the step summary.

When due, only the link is resolved first. If `source_name` and the HEAD `Content-Length` match the
manifest, only `checked_at` is updated and the 7 GB download is skipped.

Pull requests touching the Playwright script or the helper only resolve the link: no download, no
publish, and a refusal is a warning.

### Blocked vs. broken

`scripts/macos/playwright_win11_iso.py` exits with code **3** when it detects a refusal or challenge
page. Go maps that to `ErrMicrosoftRefused`. Anything else (changed selectors, timeouts) still fails
the job.

### Fallback and staleness guard

If the hosted runner is refused for an architecture, the `refresh-tart` job retries on the
self-hosted `macos-26-tart` runner (requires `vars.USE_TART_MACOS_RUNNERS == 'true'`). If both are
refused, the run stays green with a warning, **unless** `checked_at` is older than 90 days, in which
case `staleness-guard` fails the run so a human publishes an ISO manually.

## Manual publish

Use this when both runners are blocked. Download the English (United States) ISO in a browser from
the Microsoft download page, then:

```bash
export FTP_USER=… FTP_PASS=… FTP_HOST=…
ARCH=amd64          # or arm64
SRC=Win11_25H2_English_x64.iso   # Win11_25H2_English_Arm64.iso for arm64; add v2 etc. as Microsoft names it
ISO=~/Downloads/$SRC

# Fetch the current manifest (skip if none exists yet) and write the new one.
curl --ssl-reqd --user "$FTP_USER:$FTP_PASS" -o win11-$ARCH.json \
  "ftp://$FTP_HOST/private/windows11/iso/win11-$ARCH.json" || true
go run ./tools/win11iso publish --arch $ARCH --iso "$ISO" --source-name "$SRC" --manifest win11-$ARCH.json
# prints: object=…  local_path=…  pruned=…

OBJECT=win11_25h2_english_x64_$(date -u +%Y%m%d).iso   # the printed object name
# ISO first, then the manifest.
curl --ssl-reqd --ftp-create-dirs --user "$FTP_USER:$FTP_PASS" -T "$ISO" \
  "ftp://$FTP_HOST/private/windows11/iso/$OBJECT"
curl --ssl-reqd --user "$FTP_USER:$FTP_PASS" -T win11-$ARCH.json \
  "ftp://$FTP_HOST/private/windows11/iso/win11-$ARCH.json"
# Prune each object listed in `pruned` afterwards.
curl --ssl-reqd --user "$FTP_USER:$FTP_PASS" -Q "DELE /private/windows11/iso/<pruned object>" \
  "ftp://$FTP_HOST/private/windows11/iso/"
```

## Migration from the flat cache

1. Merge the refresh workflow, then run it once via `workflow_dispatch` with `adopt_legacy: true`.
   It downloads the flat `win11_25h2_english_<arch>.iso`, publishes it as a versioned object with a
   manifest (`source_name=Win11_25H2_English_<x64|Arm64>.iso`) and sets `checked_at` to the epoch so
   the next scheduled run really checks Microsoft.
2. Verify both manifests and objects exist on the FTP server.
3. Only then merge the build-workflow changes that use `manifest`.
4. Delete the flat legacy objects once every build workflow uses the manifest.
