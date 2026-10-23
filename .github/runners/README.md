# macOS Tart Self-Hosted Runner

This directory contains scripts to build and operate ephemeral macOS GitHub Actions
self-hosted runners using [Tart](https://tart.run) virtualisation.

## Scripts

| Script | Purpose |
|---|---|
| `prepare-tart-base.sh` | One-time: builds the golden Tart VM image with tooling pre-installed. |
| `create-macos-tart-runner.sh` | Ongoing: runs a pool of ephemeral runners, one job per VM clone. |

## Quick start

```bash
# 1. Build the golden image once
./prepare-tart-base.sh

# 2. Start the runner pool (loops forever)
GITHUB_REPO=myorg/myrepo ./create-macos-tart-runner.sh
```

---

## Build cache

Workflows that test Packer builds require large files — Windows 11 ISOs
(versioned objects such as `win11_25h2_english_arm64_20261023.iso`, see
[docs/windows-iso-cache.md](../../docs/windows-iso-cache.md)) and potentially other large
build dependencies such as toolchain archives. These files are several gigabytes. On runners
with a slow or metered internet connection the remote cache download dominates job run-time.

To avoid repeated downloads you can maintain a **general-purpose build cache** on the **host
machine**. The runner script mounts the directory into every VM at boot via VirtioFS
(read-write). The `download-build-cache` composite action then:

1. **Cache hit** — symlinks the file from `/Volumes/My Shared Files/build-cache/` into the
   workspace; the remote download is skipped entirely.
2. **Cache miss** — downloads the file from the configured remote build-cache backend as normal.
   CI uses FTP with explicit TLS enforced and `/private` as the remote base directory. The Azure
   Blob backend remains available through the cache actions' `storage-backend` option.

After the build the `upload-build-cache` action **copies any freshly downloaded file into the
cache** so every subsequent run on the same runner is a cache hit. No manual preparation of
individual runners is needed.

### How it works

```
First run (cache empty)
──────────────────────────────────────────────────────────────
FTP server (/private, explicit TLS)
    │  download-build-cache action
    ▼
Workflow workspace: cache/windows11/iso/win11_english_arm64.iso  (real file, resolved via win11-arm64.json)
    │  download-build-cache action  (saves a copy under the versioned object name)
    ▼
Host machine: ~/build-cache/win11_25h2_english_arm64_20261023.iso  (persists)

Every subsequent run (cache warm)
──────────────────────────────────────────────────────────────
Host machine: ~/build-cache/win11_25h2_english_arm64_20261023.iso
    │  VirtioFS (tart --dir, read-write)
    ▼
Tart VM: /Volumes/My Shared Files/build-cache/win11_25h2_english_arm64_20261023.iso
    │  download-build-cache action  (ln -sf)
    ▼
Workflow workspace: cache/windows11/iso/win11_english_arm64.iso  (symlink → no download)
```

### Set up the cache directory (one time per host)

Just create an empty directory — the first workflow run fills it:

```bash
mkdir -p ~/build-cache
```

That's it. Alternatively, if you want to pre-seed the cache to avoid the first slow download
(e.g. you have the ISO on a USB drive or another machine):

```bash
# Option A – copy from USB drive / NAS / another machine:
cp /path/to/win11_25h2_english_arm64_20261023.iso ~/build-cache/

# Option B – download once from the FTP build cache with explicit TLS:
curl --fail --ssl-reqd \
  --user "${BUILD_CACHE_FTP_USERNAME}:${BUILD_CACHE_FTP_PASSWORD}" \
  --output ~/build-cache/win11_25h2_english_arm64_20261023.iso \
  "ftp://${BUILD_CACHE_FTP_SERVER}/private/windows11/iso/win11_25h2_english_arm64_20261023.iso"
```

### Start the runner with caching enabled

Pass the `BUILD_CACHE_DIR` environment variable when launching the runner script:

```bash
BUILD_CACHE_DIR=~/build-cache \
GITHUB_REPO=myorg/myrepo \
./create-macos-tart-runner.sh

BUILD_CACHE_DIR=~/build-cache \
RUNNER_POOL_SIZE=2 VM_CPU_COUNT=4 VM_MEMORY_MB=8192 \
./create-macos-tart-runner.sh
```

The directory is mounted read-write inside every VM as `/Volumes/My Shared Files/build-cache/`.

The CI cache actions expect the repository variable `BUILD_CACHE_FTP_SERVER`, the optional
repository variable `BUILD_CACHE_FTP_PORT`, and the repository secrets
`BUILD_CACHE_FTP_USERNAME` and `BUILD_CACHE_FTP_PASSWORD`. The remote base directory defaults to
`/private`. In-repo workflows pass `storage-backend: ftp` explicitly.

Remote cache backend reference:

| Backend | `storage-backend` | Required configuration |
|---|---|---|
| FTPS | `ftp` | `BUILD_CACHE_FTP_SERVER`, optional `BUILD_CACHE_FTP_PORT`, `BUILD_CACHE_FTP_USERNAME`, `BUILD_CACHE_FTP_PASSWORD`, optional `BUILD_CACHE_FTP_BASE_DIR` |
| Azure Blob Storage | `azure` | `subscription-id` |

Callers that relied on the old implicit Azure behavior must now set `storage-backend: azure`
and pass `subscription-id`. The Terraform-managed storage account behind it is disabled by default
(`build_cache_enabled = false` in `deployments/terraform/modules/azure_gh_runner`); enable it
before using the `azure` backend.

### Set the runner CPU count or memory size and run up to 2 runners in parallel

It's a limitation by Apple to limit the concurrency of VMs to 2 per host machine.

```bash
BUILD_CACHE_DIR=~/build-cache \
RUNNER_POOL_SIZE=2 VM_CPU_COUNT=4 VM_MEMORY_MB=8192 \
./create-macos-tart-runner.sh
```

### Stopping the runner pool

Pressing `Ctrl+C` asks each active worker to cancel its current GitHub Actions workflow run
before stopping and deleting the Tart VM. The script sends `SIGINT` to the runner process inside
the VM so it stops accepting new work, finds the in-progress job by the generated runner name,
calls the workflow run cancellation API, waits for the runner to become idle or deregister, and
only then tears down the VM. By default this wait has no deadline, so the script should not exit
until the runner is released or deregistered. If you set a finite shutdown grace period and GitHub
still reports the runner as busy or its state cannot be confirmed after that period, the script
leaves that VM running instead of deleting the clone under an active job.

This clean cancellation path requires repo-scoped runners (`GITHUB_SCOPE=repo`) and a `gh` token
with `Actions: write` access to `GITHUB_REPO`. Org-scoped runner pools still stop the VM, but the
script cannot reliably know which repository owns the active job unless the pool is repo-scoped.

### Keeping cached files up to date

Cached files are large and rarely change. When a new version appears:

1. Delete (or rename) the old file in `~/build-cache/` on each host machine.
2. The next workflow run will automatically download the new file from the remote cache and
   repopulate the cache.
3. Alternatively, copy the new file directly into `~/build-cache/` to avoid any slow first run.

Windows 11 ISOs are the exception: every refresh publishes a new versioned object
(`win11_<release>_english_<arch>_<YYYYMMDD>.iso`), so the cache picks it up without manual steps.
Cache hits are verified against the manifest sha256 (a corrupt copy is evicted and downloaded
again). Old versions are not pruned from the host, though, and each one takes about 7 GB. Delete
the versions that no longer appear in the current manifests now and then:

```bash
# Lists cached Windows 11 ISOs that no current manifest references; delete them after a review.
keep="$(for arch in amd64 arm64; do
  curl --fail --silent --ssl-reqd --user "${BUILD_CACHE_FTP_USERNAME}:${BUILD_CACHE_FTP_PASSWORD}" \
    "ftp://${BUILD_CACHE_FTP_SERVER}/private/windows11/iso/win11-${arch}.json" |
    python3 -c 'import json,sys; print("\n".join(h["object"] for h in json.load(sys.stdin)["history"]))'
done)"
for f in ~/build-cache/win11_*_[0-9]*.iso; do
  [ -e "$f" ] || continue
  printf '%s\n' "$keep" | grep -qxF "$(basename "$f")" || echo "unreferenced: $f"
done
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `BUILD_CACHE_DIR` | _(unset)_ | Absolute path on the **host** to the build cache directory. When unset, no directory is mounted and jobs download all files normally. |
| `VM_BASE_IMAGE` | `tahoe-runner` | Tart image used as the base for each ephemeral clone. |
| `VM_CLONE_PER_RUN` | `true` | Clone a fresh VM for every runner cycle (recommended). |
| `GITHUB_REPO` | `csautter/dev-alchemy` | `owner/repo` for runner registration. |
| `GITHUB_SCOPE` | `repo` | `repo` or `org`. |
| `RUNNER_LABELS` | `macos,tart,arm64,macos-26-tart` | Comma-separated runner labels. |
| `MAX_RUNS` | `0` (infinite) | Stop the loop after this many runner cycles. |
| `GITHUB_CANCEL_RUN_ON_SHUTDOWN` | `true` | Cancel the workflow run assigned to a busy runner before VM teardown on `Ctrl+C`, `TERM`, or broken runner SSH sessions. |
| `GITHUB_FORCE_CANCEL_RUN_ON_SHUTDOWN` | `false` | Request force-cancel if the normal cancellation has not settled. This bypasses workflow conditions, so keep it opt-in for jobs with important cleanup. |
| `RUNNER_SHUTDOWN_GRACE_SECONDS` | `0` | Maximum wait for GitHub to report that the runner is idle or deregistered before preserving the VM for manual cleanup. `0` waits indefinitely, which is the default clean shutdown behavior. |
| `RUNNER_FORCE_CANCEL_AFTER_SECONDS` | `30` | Delay before force-cancel is attempted during shutdown. Set to `0` to disable force-cancel timing. |
| `RUNNER_SHUTDOWN_POLL_SECONDS` | `5` | Poll interval while waiting for a canceled job to release the runner. Must be at least `1`; non-numeric or lower values fall back to `5` to avoid tight GitHub API polling. |
