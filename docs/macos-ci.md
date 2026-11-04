# macOS validation

macOS validation has three tiers. Linux checks run for the broad set of build,
deploy and installer changes. Real Macs cover the platform behavior that those
checks cannot reproduce.

| Tier | Runner | Coverage | Trigger |
| --- | --- | --- | --- |
| A: Darwin profile | `ubuntu-24.04` for amd64, `ubuntu-24.04-arm` for arm64 | Packer validation and QEMU argument snapshots for Windows 11, Ubuntu Server and Ubuntu Desktop; Darwin Go compilation; shell portability and dependency selection; UTM bundle snapshots | Build/deploy/installer PR paths and manual dispatch |
| B: quick check | GitHub-hosted `macos-26` | Homebrew installation, Darwin unit tests, BSD shell behavior, all six real QEMU startup probes, UTM bundle creation and an attempt to register bundles | macOS paths or `ci:macos` |
| C: full build | `macos-26-tart` when enabled, otherwise `macos-26` | Full arm64 installations and UTM deployment/startup smoke tests; amd64 startup probes | Weekly, release PRs, runner changes, `ci:macos-full`, or manual dispatch |

Full amd64 installations and their UTM smoke tests run on the first Monday of
each month and on release-please PRs. Other full-tier runs use startup probes
for amd64. The weekly run is replaced by the monthly run on that first Monday.
The monthly schedule wakes during the first seven days; its selector only
starts Mac jobs on Monday. Scheduled failures create or update the open issue
named **Scheduled macOS build failed**.

Set the repository variable `USE_TART_MACOS_RUNNERS=true` to use Tart for tier C.
Tier B always uses hosted Macs. The timeout is 15 minutes per Linux profile job,
30 minutes per quick job and 260 minutes per full job. Check the first hosted
runs to confirm the actual duration, especially dependency installation and
cache downloads.

## Requesting real Mac checks

Apply `ci:macos` when a change deserves a real Mac startup check even if it does
not touch a macOS path. Apply `ci:macos-full` when installation or UTM boot
behavior needs validation. Labels can be applied to any PR; subsequent pushes
keep requesting the labeled tier.

The real-Mac workflow starts with a small Linux selector on PR events. It
paginates the PR file list, including previous names for renamed files. It
starts tier B for:

- `scripts/macos/**`
- `build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh`
- `pkg/build/macos-*.go` and `pkg/build/qemu-arm64.go`
- `pkg/deploy/macos-*.go` and `pkg/deploy/utm*`
- `deployments/utm/**`
- `test/macos-profile/golden/**`
- `.github/workflows/test-build-macos.yml`

Changes under `.github/runners/**` request tier C. Shared, Linux and Windows
changes do not start Mac jobs unless a label or release branch requests them.
The selector intentionally runs without a workflow-level PR path filter so
label events can override paths.

Tier A needs no cache credentials or repository secrets and works on fork PRs.
The real-Mac Windows probes use the existing private ISO cache; Microsoft ISO
downloads remain disabled in build jobs. Those probes need the repository's
existing cache access, which is unavailable to untrusted fork PRs.

## Running and updating snapshots

Install Packer (CI uses 1.16.2), its QEMU plugin, Python 3, Go and the fixture
tools: `qemu-img`, `xorriso`, `mkfs.vfat`, `mcopy`, `swtpm`, `envsubst`, `uuidgen`, and
`rsync`. On Ubuntu these are supplied by `qemu-utils`, `dosfstools`, `mtools`,
`swtpm`, `gettext-base`, `uuid-runtime`, `rsync`, and `xorriso`. Packer creates a
cloud-init CD from the Ubuntu template's `cd_files` even with fixture ISOs.

```sh
make test-macos-profile
make test-macos-profile-shell test-macos-profile-go
python3 test/macos-profile/test-routes.py
# Limit the Packer checks to one guest architecture:
make test-macos-profile MACOS_PROFILE_ARCH=arm64
# After reviewing an intentional template or bundle change:
make test-macos-profile-golden
```

The Packer harness copies each template and its adjacent fixtures into a
temporary directory. It validates `host_os=darwin`, `host_arch=arm64` and
`CI=true`, including when the guest is amd64. A QEMU executable shim records the
original argv and waits until the harness stops the process group. No guest
boots, firmware executes or installer image downloads in tier A. The fixture
ISO and firmware files are deliberately not bootable.

Snapshots normalize temporary paths, allocated network ports and generated
UUIDs/MAC addresses. Packer emits option groups from a Go map; snapshots sort
option names while preserving every value and the order of repeated options.
They do not translate acceleration, CPU, display or machine settings. Review
all snapshot changes in `test/macos-profile/golden/` before committing them.
Changing a Darwin template argument fails tier A until its snapshot is updated;
committing that update automatically requests tier B.

UTM tests parse `config.plist` with Python's `plistlib`, compare all six
configurations and verify disk copies and repeatable bundle creation. Set
`SAILWRIGHT_UTM_VM_DIR` to keep bundles outside the normal UTM directory. Create,
start/stop inspection, destroy and provisioning all use that destination; see
[UTM bundle location](./managed-application-data.md#utm-bundle-location).
Without the override, the existing destination is retained. The tier B check
attempts AppleScript registration without starting guests. If GUI automation
is unavailable or times out, it reports that limitation and retains bundle
validation as coverage; confirm registration on a Mac when reviewing such a
run. A successful open request alone does not prove the guest will boot.

The shell tests extract production functions without running installers or
builds, then supply command stubs. Native mode (`--native`) keeps real `uname`
and BSD `stat`. Portability lint has exact-line exceptions for Linux-only KVM
probe commands in the shared Windows script. The Linux arm64 job also runs
syntax and shell tests with `bash:3.2`, matching the system Bash listed in the
[hosted macOS image inventory](https://github.com/actions/runner-images/blob/main/images/macos/macos-26-arm64-Readme.md).

## Coverage limits

Tier A does not execute a Darwin binary, Cocoa, Homebrew bottles, BSD tools,
AppleScript, TCC or APFS. Tier B exercises real QEMU startup, but it does not
complete guest installation or boot guests through UTM. Only tier C checks
installation followed by UTM guest startup.

Hardware acceleration through HVF is outside these checks. The existing
`is_ci` setting keeps Darwin CI builds on TCG because Tart VMs cannot provide
nested virtualization. The Linux profile does not substitute Linux-built
images for macOS build results. An optional Linux QEMU/KVM probe is not enabled;
QEMU compatibility and real firmware execution remain tier B checks.
