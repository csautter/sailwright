# Installation

Sailwright ships as two binaries per release: the `sailwright` CLI and the
host-side [Agent Gateway](./agent-gateway.md) `sailwright-gateway`. Pick one of
the ways below; all of them install the same release archives from
[GitHub Releases](https://github.com/csautter/sailwright/releases).

| Platform | Recommended | Fallback |
| --- | --- | --- |
| macOS | [Homebrew](#homebrew) | [Install script](#install-script) |
| Linux | [Install script](#install-script) | [Homebrew](#homebrew) |
| Windows | [Install script](#install-script) | [Manual install](#manual-install) |

After installing, set up the host dependencies with `sailwright install`; see
[Host dependencies](#host-dependencies).

## Host dependencies

Run `sailwright install` after installing the CLI. On Ubuntu 24.04, Ubuntu
26.04, and Debian 13 it installs Ansible through the distribution's `pipx`,
using an isolated environment under `/opt/sailwright/pipx`. Commands including
`ansible-playbook` and `ansible-galaxy` are linked into `/usr/local/bin`.
The full Ansible collection bundle and `pywinrm` remain available; pip never
modifies the distribution's Python environment.

The dependency installers and `sailwright install` require ansible-core
**2.19.0 or newer** and verify that `ansible-playbook --version` starts
successfully. This version detects rejected sudo passwords during local
provisioning. Self-installed older Ansible still triggers a warning for `-K`.
If validation finds an older executable, check `command -v ansible-playbook`
and put `/usr/local/bin` before its directory on `PATH`, or activate an
environment with a supported ansible-core.

Rerun `sailwright install` to refresh Linux dependencies to the latest Ansible
compatible with the distribution Python. To remove its isolated Ansible
installation and command links:

```bash
sudo env PIPX_HOME=/opt/sailwright/pipx PIPX_BIN_DIR=/usr/local/bin pipx uninstall ansible
```

This does not remove other host dependencies or an Ansible package installed
separately through apt.

## Homebrew

On macOS or Linux:

```bash
brew install csautter/sailwright/sailwright
brew install csautter/sailwright/sailwright-gateway   # optional
```

Update with `brew upgrade sailwright` (and `brew upgrade sailwright-gateway`
if you installed it).

See [Homebrew Tap](./homebrew-tap.md) for how the tap is maintained.

## Install script

macOS / Linux:

```bash
curl -fsSL https://github.com/csautter/sailwright/releases/latest/download/install.sh | sh
```

Windows (PowerShell 5.1 or 7):

```powershell
irm https://github.com/csautter/sailwright/releases/latest/download/install.ps1 | iex
```

The URLs always serve the scripts from the latest release. The scripts:

1. detect the OS and architecture (`amd64` or `arm64`),
2. resolve the latest release tag from the GitHub releases page (avoiding the
   GitHub REST API rate limit),
3. download `sailwright_<version>_checksums.txt` and, if `cosign` is on your
   `PATH`, verify its keyless signature against the release workflow identity,
4. download the archives, check each SHA-256 against the checksums file and
   **abort without installing anything** on a mismatch,
5. copy the binaries into the install directory.

Nothing runs with `sudo` or admin rights. The scripts only download from
GitHub Releases; when `cosign` is installed, it also contacts the public
Sigstore services to verify the signature.

| | macOS / Linux | Windows |
| --- | --- | --- |
| Default directory | `~/.local/bin` | `%LOCALAPPDATA%\Programs\sailwright` |
| `PATH` | prints a hint if the directory is missing | adds the directory to the user `PATH` |

Use `sailwright update` to update to the latest release. Older installations
without this command need the install command above once more.

### Options

| Setting | `install.sh` | `install.ps1` | Environment variable |
| --- | --- | --- | --- |
| Install a specific release | `--version v0.20.0` | `-Version v0.20.0` | `SAILWRIGHT_VERSION` |
| Install into another directory | `--install-dir <dir>` | `-InstallDir <dir>` | `SAILWRIGHT_INSTALL_DIR` |
| Skip `sailwright-gateway` | `--no-gateway` | `-NoGateway` | `SAILWRIGHT_NO_GATEWAY=1` |

When piping, pass flags to `install.sh` after `sh -s --`, and use the
environment variables for `install.ps1`:

```bash
curl -fsSL https://github.com/csautter/sailwright/releases/latest/download/install.sh | sh -s -- --no-gateway
```

```powershell
$env:SAILWRIGHT_NO_GATEWAY = '1'
irm https://github.com/csautter/sailwright/releases/latest/download/install.ps1 | iex
```

To read the script before running it, download it first:

```bash
curl -fsSLO https://github.com/csautter/sailwright/releases/latest/download/install.sh
less install.sh
sh install.sh --install-dir "$HOME/.local/bin"
```

### Uninstall

Delete the two binaries from the install directory. On Windows, stop
`sailwright-gateway.exe` first, and also remove the directory from your user `PATH` (Settings → System → About → Advanced
system settings → Environment Variables).

## Manual install

Download `sailwright_<version>_<os>_<arch>` (`.tar.gz` on macOS/Linux, `.zip`
on Windows) and `sailwright_<version>_checksums.txt` from the release, verify
the archive, and extract it:

```bash
(
  set -euo pipefail
  TAG="$(curl -fsSL https://api.github.com/repos/csautter/sailwright/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  test -n "$TAG"
  VERSION="${TAG#v}"
  curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_linux_amd64.tar.gz"
  curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_checksums.txt"

  if command -v cosign >/dev/null 2>&1; then
    curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_checksums.txt.sig"
    curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_checksums.txt.pem"
    cosign verify-blob \
      --certificate "sailwright_${VERSION}_checksums.txt.pem" \
      --signature "sailwright_${VERSION}_checksums.txt.sig" \
      --certificate-identity "https://github.com/csautter/sailwright/.github/workflows/release-binaries.yml@refs/heads/main" \
      --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
      "sailwright_${VERSION}_checksums.txt"
  fi

  grep "sailwright_${VERSION}_linux_amd64.tar.gz" "sailwright_${VERSION}_checksums.txt" | sed 's| dist/| |' | sha256sum -c -
  tar -xzf "sailwright_${VERSION}_linux_amd64.tar.gz"
  ./sailwright --help
)
```

The example stops on download or verification failure. When `cosign` is on
your `PATH`, it downloads the signature and certificate and verifies the
checksums file before extracting or running the binary. See
[SECURITY.md](../SECURITY.md#signature-verification) for details. The subshell
keeps the error-handling settings local to the example.

The README's [Manual install](../README.md#manual-install) section has a
Windows example as well.

## Host dependencies

`sailwright install` installs the host tools Sailwright needs on the current
operating system:

| Host | Script it runs |
| --- | --- |
| macOS | [scripts/macos/sailwright-install-dependencies.sh](../scripts/macos/sailwright-install-dependencies.sh) |
| Ubuntu / Debian | [scripts/linux/sailwright-install-dependencies.sh](../scripts/linux/sailwright-install-dependencies.sh) |
| Windows | [scripts/windows/sailwright-self-setup.ps1](../scripts/windows/sailwright-self-setup.ps1) |

On every host, `sailwright install --with-go` also bootstraps the Go toolchain.

### Windows host dependencies

Run `sailwright.exe install` from your normal shell; Windows prompts for
elevation through UAC if needed. It installs the tools every workflow needs
(Chocolatey, Git, make, Packer, Azure CLI, Python, Cygwin with Ansible).
Further components are optional:

| Component | Flag | Needed for |
| --- | --- | --- |
| VirtualBox | `--virtualbox` | VMs with the VirtualBox engine |
| Hyper-V | `--hyperv` | building, creating or running VMs with the Hyper-V engine |
| Vagrant | `--vagrant` | creating, starting, stopping or destroying VMs (Sailwright drives Windows VMs through Vagrant) |

Hyper-V and Vagrant are not needed for local use such as
`sailwright provision local`. Without their flags, an interactive terminal
explains each one and asks whether to add it (default: no). Non-interactive
runs, such as CI or the agent gateway, skip both and print a hint. Pass
`--hyperv=false` or `--vagrant=false` to skip the question. To create and run
Hyper-V VMs, add both:

```powershell
sailwright.exe install --hyperv --vagrant
```

Hyper-V setup enables the optional features on Windows 10/11 Pro, Enterprise
and Education, or installs the Hyper-V role on Windows Server. It needs
hardware virtualization (nested virtualization when Windows itself runs in a
VM) and stops before the package installs when the edition or processor cannot
run Hyper-V. The installer never restarts Windows on its own: when it reports a
pending restart, restart Windows and run `sailwright.exe install --hyperv`
again. Once Hyper-V is active it reports whether the Hyper-V management service
runs and whether the Default Switch exists. `--hyperv` also installs `oscdimg`
from the Windows ADK Deployment Tools and adds it to the machine `PATH`;
Packer needs it to build Hyper-V images. The ADK installer is downloaded from
Microsoft and only runs when it carries a valid Microsoft Authenticode
signature.

Sailwright VMs on Hyper-V expose virtualization extensions to the guest by
default. When Windows itself runs in a VM whose hypervisor cannot pass them on,
Hyper-V refuses to start such a VM; `sailwright create` and `sailwright start`
then turn the extensions off for that VM and retry once. The fallback is
remembered in the VM's Vagrant directory (file
`sailwright-nested-virtualization-unsupported`), so later starts go without the
extensions right away; delete the file to try again. Set
`VAGRANT_HYPERV_NESTED_VIRTUALIZATION=false` to skip the first attempt, or
`true` to always try with the extensions.

Vagrant is installed in a pinned version through Chocolatey and added to the
`PATH`.

## Updating

Check the installed version with `sailwright --version`. `sailwright version`
also shows OS/architecture, installation type, the effective automatic
check setting, and the signature verification policy.

- **Homebrew:** `brew upgrade sailwright` and, if installed,
  `brew upgrade sailwright-gateway`. Self-update refuses Homebrew paths,
  including symlinks into the Cellar. Scoop and winget installations must
  also be updated through their package manager.
- **Install script or manual release:** `sailwright update`. The adjacent
  gateway is updated too; restart it if it is running. Use `--no-gateway`
  to update only the CLI.
- **Development build / go install:** rebuild from source or install a
  release using the install script. Development builds cannot self-update.

The updater displays the version change and a release-notes link before
asking for confirmation. Without a terminal on stdin, pass `--yes`:

```bash
sailwright update --yes
sailwright update --check
sailwright update --version v1.2.3
sailwright update --yes --require-signature
```

`--check` installs nothing: exit **0** means already current (or newer than
latest), **2** means an update is available, **1** means an error. An explicit
`--version` permits a downgrade only with a terminal on stdin and an explicit
confirmation, even with `--yes`; otherwise the updater never downgrades.
Only stable release versions are supported. Downgrading to a release older
than the first one with `sailwright update` removes the command; rerun the
install script to return to a newer release.

The first release containing `update` can update itself. For an earlier
release, rerun the install script once to gain the command.
The trust model and replacement strategy are recorded in
[ADR 0007](./adr/0007-cli-self-update.md).

### Verification

- Downloads use the same asset names and SHA-256 checksums as the install
  scripts, including checksum entries prefixed with `dist/`.
- **Without cosign, only checksums are checked.** The checksum manifest comes
  from the same GitHub release as the archives, so this detects corrupted
  downloads but does not prove the release was published by the Sailwright
  release workflow. The updater prints a warning in that case.
- If cosign is installed, the manifest signature must verify against the
  release workflow identity before installation; this can contact public
  Sigstore services.
- To require the signature, pass `--require-signature` or set
  `"require_signature": true` in `update.json` in the config directory. The
  update then fails when cosign is unavailable. This is recommended.
  Invalid settings stop updates rather than bypassing this policy.
- Downloads reject insecure redirects, except HTTP loopback redirects when
  using an explicit local fixture source.

### Replacement and recovery

- Both archives are verified and staged before either binary is replaced.
  Files are staged in the installation directory with the existing
  executable permissions.
- Each replacement uses an atomic rename on Linux/macOS. Windows renames the
  old executable to `.old`; the next CLI invocation removes these backups.
- A failed replacement restores previous binaries; an in-use Windows gateway
  may require stopping it before retrying.
- The target directory must be writable; the updater never invokes sudo or
  requests administrator rights.
- The `.update-lock` file remains in the installation directory; its OS lock
  is released automatically when the updater exits, including after a crash.
  Do not delete it while an updater is running.
- On Unix, recover any interrupted update before deleting leftover `.old`
  backups and retrying.

## Optional update notices

Automatic checks are **off by default**. Enable or disable them explicitly:

```bash
sailwright update --auto-check on
sailwright update --auto-check-interval 1h
sailwright update --auto-check off
```

The interval uses Go duration syntax, defaults to **1h**, and must be at
least **15m**. Settings commands work offline. Alternatively set
`SAILWRIGHT_UPDATE_CHECK=1`; `SAILWRIGHT_NO_UPDATE_CHECK=1` always disables
checks, including when the first variable or the saved setting enables them.

When enabled, an eligible CLI invocation issues at most one GET per interval
to `https://github.com/csautter/sailwright/releases/latest`, reads its release
tag redirect, and stops without fetching the release page or using the API.
The application adds only `User-Agent: sailwright/<version>`; no configuration,
project data, machine identifiers, or credentials are sent. The request runs
alongside the command with a two-second timeout. When the command finishes,
the CLI waits at most 150 ms for a pending request. A result completed by then
can produce a one-line notice on **stderr**; a cached result can be shown on a
later invocation. There is no automatic installation, daemon, or background service.

No check or notice occurs for `update`, `version`, `--version`, `help`,
completion, development builds, when stderr has no TTY, or when `CI` is set.
A completed check starts the interval, including a failed one; failures are
silent. A check that had not completed when the command exited is retried
after five minutes, so quick commands do not suppress checks for a whole
interval. Notices for an
available version occur at most once per interval; stdout stays unchanged.
The gateway does not perform checks.

Settings are stored in `update.json` (for example
`{"enabled":true,"interval":"1h"}`) in the existing configuration directory;
check and attempt times, last seen version, and notice time are in `update-check.json` in
the existing cache directory. Defaults are:

| Platform | Configuration directory | Cache directory |
| --- | --- | --- |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/dev-alchemy` | `${XDG_DATA_HOME:-~/.local/share}/dev-alchemy/cache` |
| macOS | `~/Library/Application Support/dev-alchemy` | `~/Library/Application Support/dev-alchemy/cache` |
| Windows | `%APPDATA%\dev-alchemy` | `%LOCALAPPDATA%\dev-alchemy\cache` |

The existing `DEV_ALCHEMY_CONFIG_DIR`, `DEV_ALCHEMY_APP_DATA_DIR`, and
`DEV_ALCHEMY_CACHE_DIR` overrides apply. `sailwright version` shows the resolved
settings path and effective enablement, including environment overrides. An
unreadable or invalid `update.json` disables checks, even when
`SAILWRIGHT_UPDATE_CHECK=1` is set; `sailwright version` reports the problem, and `sailwright update --auto-check on|off` replaces the
file with valid settings.

For local integration tests only, `SAILWRIGHT_UPDATE_BASE_URL` replaces the
repository URL for the latest redirect and release downloads (for example,
`http://127.0.0.1:8765`). Only http(s) URLs on a loopback address are
accepted, and `sailwright update` prints a warning while the override is set.
Signature identity remains pinned to the public release workflow. Never use
this override for normal updates. The fixture
tests in `test/selfupdate` exercise the installers and updater with real
versioned binaries on all three operating systems.
