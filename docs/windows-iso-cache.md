# Windows 11 ISO cache

CI never downloads Windows 11 from Microsoft inside a build job. One workflow,
[Refresh Windows 11 ISO](../.github/workflows/refresh-windows-iso.yml), resolves and downloads the ISO
from Microsoft, and only when a check is worthwhile. It publishes ISOs under versioned names to the
FTP build cache and keeps a manifest that points to the current one. Every build job resolves the
ISO through that manifest. (The separate
[download_win_11.ps1 test](../.github/workflows/test-download-win11.yml) still resolves a link for
the standalone PowerShell script; it neither downloads nor publishes anything.)

The design decisions are recorded in
[ADR 0006](./adr/0006-versioned-windows-iso-cache.md).

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
  "announced_release": "25H2",
  "history": [{"object": "…", "sha256": "…", "published_at": "…"}]
}
```

`build` is optional and currently empty. `announced_release` is the newest release the release
notes announced at the last successful check (see the due rule). Readers reject manifests with an
unknown `schema`, an object name outside `[A-Za-z0-9._-]` or a malformed sha256.

The current object plus the two previous ones are kept (`history`); older objects are deleted after
the new manifest is uploaded. The object the manifest points to is never deleted.

**Publishing is atomic.** Every upload (ISO and manifest) goes to a temporary name in the same
directory, is size-checked and then renamed into place (`RNFR`/`RNTO`). The ISO is in place before
the manifest that names it, so readers see either the old or the new manifest and never a partial
file. If the server refuses to rename onto an existing file, the workflow deletes the old manifest
and renames, with a warning; the manifest is then missing only between those two FTP commands.

**Objects are immutable.** An object that a manifest references is never replaced. If a download
has the same sha256 as an object in `history`, that object is reused instead of storing a copy; if
it is the current object, only `checked_at` changes. Different bytes under a name that is already in
`history` (same-day re-publish of changed media) fail the run. An object under the new name that no
manifest references is a leftover from an interrupted run and is replaced.

## How build jobs use it

Build workflows list the ISO as
`{"local-path":"./.dev-alchemy/cache/windows11/iso/win11_english_<arch>.iso","manifest":"windows11/iso/win11-<arch>.json","blob-name":"win11_25h2_english_<arch>.iso"}`.
The `download-build-cache` action fetches the manifest, downloads `windows11/iso/<object>`, verifies
the sha256 (deleting the file on mismatch) and writes the manifest next to the ISO as
`<local-path>.json`. A file already in the workspace or in the Tart runner's local cache is used
only if its sha256 matches the manifest; a corrupt runner-cache copy is evicted and downloaded
again. The runner cache is keyed by the versioned object name, so a new ISO never hits a stale
copy. Old versions are not pruned from the runner host, see
[the runner README](../.github/runners/README.md#keeping-cached-files-up-to-date).

`blob-name` is a temporary migration fallback: only while the manifest does not exist yet, the
action downloads the flat legacy object instead (without a sidecar, so the next run with a manifest
replaces it). It is removed after the migration, see below.

`upload-build-cache` skips manifest entries ("managed by refresh workflow"). The manifest feature
is FTP only; the legacy Azure backend fails with a clear message.

Build jobs set `SAILWRIGHT_WIN11_ISO_OFFLINE=1` (`1` or `true`). If the ISO is missing, the build
fails immediately with "Windows 11 ISO missing at …; Microsoft download disabled in CI" instead of
falling back to Playwright. `build/packer/windows/windows11-qemu.sh` honors the same variable when
it is run directly.

Locally the ISO lives at `win11_english_<arch>.iso`. A legacy `win11_25h2_english_<arch>.iso` is
renamed once, during dependency reconciliation (or by the QEMU/UTM shell scripts).

### Windows 11 ISO missing: "Microsoft download disabled in CI"

This error means `SAILWRIGHT_WIN11_ISO_OFFLINE` is `1` or `true` and the ISO is missing from
`<cache dir>/windows11/iso/win11_english_<arch>.iso`. In CI, check the `download-build-cache` step
and confirm that the manifest and referenced ISO are available in the FTP build cache. Keep
offline mode enabled so build jobs continue to use the managed cache.

When reproducing a workflow locally, unset `SAILWRIGHT_WIN11_ISO_OFFLINE` to allow a download
from Microsoft, or place the ISO at the cache path above.

## When Microsoft is contacted ("due" rule)

The workflow runs every Monday and evaluates the rule offline first:

1. No manifest: due.
2. `workflow_dispatch` with `force: true`: due.
3. The release notes page lists a newer `Version NNH[12]` than `manifest.release`, and that
   announcement differs from `manifest.announced_release`: due. Each announcement triggers one
   check only, so a release without general ISO media (for example a device-specific release)
   does not contact Microsoft every week; the age rule picks up media published later. A failed
   lookup is ignored and the age rule applies.
4. `now - checked_at >= 28 days`: due (catches `v2` media refreshes).
5. Otherwise skipped; the reason is written to the step summary.

Before recording an unchanged ISO as successfully checked, the refresh verifies that the referenced
remote object exists with the expected size. Missing objects are restored from bytes verified against
the manifest digest before the manifest is published. An inconsistent existing size fails the refresh.

When due, only the link is resolved first. The ISO counts as unchanged, and only `checked_at` is
updated, if Microsoft's published sha256 equals the manifest's, or (without a published sha256) if
`source_name` and the size match. The size comes from a HEAD request, or from a one-byte ranged GET
when HEAD fails. If the size cannot be determined, the ISO is downloaded; a download identical to
the current object is still reported as `unchanged` and not published again.

The Playwright script reads Microsoft's sha256 for the English (United States) ISO from the
download page. The step summary says whether the published ISO was verified against it; a missing
hash produces a warning.

Pull requests touching the Playwright script or the helper only resolve the link: no download, no
publish, and a refusal is a warning.

### Blocked vs. broken

`scripts/macos/playwright_win11_iso.py` exits with code **3** when it detects a refusal or challenge
page. Go maps that to `ErrMicrosoftRefused`. Anything else (changed selectors, timeouts) still fails
the job. FTP errors other than "not found" (authentication, TLS, network) also fail the job instead
of being treated as an empty cache.

### Fallback and staleness guard

If the hosted runner is refused for an architecture, the `refresh-tart` job retries on the
self-hosted `macos-26-tart` runner (requires `vars.USE_TART_MACOS_RUNNERS == 'true'`). If both are
refused, the run stays green with a warning, **unless** `checked_at` is older than 90 days, in which
case `staleness-guard` fails the run so a human publishes an ISO manually.

## Manual publish

Use this when both runners are blocked. Download the English (United States) ISO in a browser from
the Microsoft download page, then:

```bash
set -euo pipefail
export FTP_USER=… FTP_PASS=… FTP_HOST=…
ARCH=amd64                       # or arm64
SRC=Win11_25H2_English_x64.iso   # Win11_25H2_English_Arm64.iso for arm64; add v2 etc. as Microsoft names it
ISO=~/Downloads/$SRC
DIR=/private/windows11/iso
cache_curl() { curl --fail --ssl-reqd --user "$FTP_USER:$FTP_PASS" "$@"; }

# 1. Fetch the current manifest. Exit code 78 (or 19) means none exists yet; stop on anything else.
rm -f win11-$ARCH.json
if cache_curl -o win11-$ARCH.json "ftp://$FTP_HOST$DIR/win11-$ARCH.json"; then
  :
else
  status=$?
  case "$status" in
    19|78) rm -f win11-$ARCH.json ;;
    *) echo "Manifest fetch failed (curl exit code $status)" >&2; exit "$status" ;;
  esac
fi

# 2. Write the new manifest. Prints object=…, existing=…, unchanged=… and pruned=….
go run ./tools/win11iso publish --arch $ARCH --iso "$ISO" --source-name "$SRC" \
  --manifest win11-$ARCH.json | tee publish.out
OBJECT=$(sed -n 's/^object=//p' publish.out)

# 3. Upload under a temporary name and rename into place: the ISO first (skip it when
#    existing=true, the object is already published), then the manifest.
if ! grep -qx 'existing=true' publish.out; then
  cache_curl --ftp-create-dirs -T "$ISO" "ftp://$FTP_HOST$DIR/$OBJECT.upload"
  cache_curl -o /dev/null -Q "RNFR $DIR/$OBJECT.upload" -Q "RNTO $DIR/$OBJECT" "ftp://$FTP_HOST$DIR/"
fi
cache_curl -T win11-$ARCH.json "ftp://$FTP_HOST$DIR/win11-$ARCH.json.upload"
cache_curl -o /dev/null -Q "RNFR $DIR/win11-$ARCH.json.upload" -Q "RNTO $DIR/win11-$ARCH.json" \
  "ftp://$FTP_HOST$DIR/"

# 4. Delete the objects that fell out of the retention window.
for obj in $(sed -n 's/^pruned=//p' publish.out); do
  cache_curl -o /dev/null -Q "DELE $DIR/$obj" "ftp://$FTP_HOST$DIR/"
done
```

If the server refuses `RNTO` onto the existing manifest, delete it first (`-Q "DELE $DIR/win11-$ARCH.json"`)
and repeat the rename.

## Migration from the flat cache

The build workflows already resolve the ISO through the manifest, and fall back to the flat legacy
object through `blob-name` while no manifest exists. Merging is therefore safe before the adoption.

1. After the merge, run the refresh workflow once via `workflow_dispatch` with
   `adopt_legacy: true`. It downloads the flat `win11_25h2_english_<arch>.iso`, publishes it as a
   versioned object with a manifest (`source_name=Win11_25H2_English_<x64|Arm64>.iso`) and backdates
   `checked_at` by 28 days, so the next scheduled run checks Microsoft but a refused first run does
   not trip the 90-day staleness guard.
2. Verify both manifests and objects exist on the FTP server. Build logs now show "Manifest points
   to …" instead of "Falling back to the legacy object".
3. Remove `"blob-name":"win11_25h2_english_<arch>.iso"` from the build workflows and from
   `assertWindowsISOResolvedViaManifest` in `test/workflowcheck/build_workflows_test.go`.
4. Delete the flat legacy objects, then remove the `adopt_legacy` input and the `adopt` mode of the
   refresh action.

Until step 4, rolling back only requires reverting the build-workflow descriptors: the legacy
objects are still in place.

## Tests

- `go test ./pkg/build/... ./tools/...`: naming, due rule, manifest validation, deduplication,
  same-name conflicts, resolve decisions (with stubbed Microsoft calls), legacy rename.
- `bash .github/actions/refresh-windows-iso/tests/refresh-test.sh`: `refresh.sh` against a fake FTPS
  server (interrupted uploads, atomic replacement, leftovers, retention, adoption, FTP errors).
- [Test Build Cache Actions](../.github/workflows/test-cache-actions.yml): manifest resolution in the
  Bash and PowerShell cache actions, including corrupt cache hits and the legacy fallback.
- `python3 -m unittest discover -s test/scripts`: parsing of Microsoft's sha256 table.
