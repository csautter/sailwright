# What gets installed

There are two separate steps: installing the CLI and running `sailwright install`
to install host dependencies. Read the relevant sections before running either.
The inventory below follows the scripts in this checkout; older releases can
install different packages. Package managers also install transitive dependencies
and may update shared packages. There is no automatic Sailwright uninstaller.

## CLI install scripts (all platforms)

[`install.sh`](../scripts/install.sh) and [`install.ps1`](../scripts/install.ps1)
download release archives and SHA-256 checksums from GitHub. If `cosign` is
already available, they also download and verify the checksum signature and
certificate. They replace `sailwright` and, unless disabled with `--no-gateway`
or `-NoGateway`, `sailwright-gateway`. Downloads and extraction use a temporary
directory that is cleaned up. Updates stage files beside the destination;
a failed recovery may leave backups for manual restoration. Windows can retain
`sailwright.exe.*.old` or `sailwright-gateway.exe.*.old` while a binary is running.

| Change | Linux / macOS | Windows |
| --- | --- | --- |
| Default destination | `~/.local/bin` | `%LOCALAPPDATA%\Programs\sailwright` |
| Privileges | current user, no sudo | current user, no elevation |
| Persistent PATH | prints a shell-profile hint; does not edit the profile | appends the directory to the raw `Path` value in `HKCU\Environment` as `REG_EXPAND_SZ`, preserving `%VAR%` references |
| Current shell PATH | unchanged | appends the directory |
| Other settings | none | briefly sets and removes the user variable `SAILWRIGHT_INSTALL_REFRESH` to notify Explorer of environment changes |

Neither script registers services, scheduled tasks or shell integrations, nor
installs host dependencies. Installing the gateway binary does not start it.
The Windows script runs the installed `sailwright.exe --version`; a failed
start is reported after the files and PATH have been installed. See
[Managed / company machines](./troubleshooting.md#managed--company-machines).
The CLI may initialize its own application data when executed; see [User data](#user-data).


For older CLI releases that reject `--version`, the Windows installer checks
`--help` instead to confirm that the installed executable can start.

### Remove the CLI

1. Stop running Sailwright commands and the gateway.
2. For script/manual installs on Unix:

   ```bash
   rm -f "$HOME/.local/bin/sailwright" "$HOME/.local/bin/sailwright-gateway"
   ```

   Substitute your custom install directory. Remove only the PATH line you
   added to `~/.zprofile`, `~/.profile` or `~/.bashrc`; keep `~/.local/bin` on
   PATH if other programs use it.
3. For script/manual installs on Windows:

   ```powershell
   $installDir = Join-Path $env:LOCALAPPDATA 'Programs\sailwright'
   Remove-Item -LiteralPath "$installDir\sailwright.exe", "$installDir\sailwright-gateway.exe" -ErrorAction SilentlyContinue
   ```

   Adjust `$installDir` for a custom destination. Inspect and remove any
   retained `.old` backups of these two binaries. In **Advanced system settings
   → Environment Variables → User variables → Path**, delete only the entry
   for that install directory. Keep other entries and `%VAR%` references.
   Delete the directory if empty. Open a new terminal to refresh PATH.
4. For Homebrew installs instead:

   ```bash
   brew uninstall sailwright sailwright-gateway
   brew untap csautter/sailwright
   ```

   Omit the gateway if not installed; keep the tap if you still use another
   formula from it. These commands do not remove host dependencies or user data.

## Windows host dependencies

[`sailwright-self-setup.ps1`](../scripts/windows/sailwright-self-setup.ps1),
invoked by `sailwright install`, requests administrator rights through UAC.
It bootstraps Chocolatey from `community.chocolatey.org` if missing and uses
its configured package sources. The bootstrap sets execution policy to
`Bypass` for that process; it does not persistently change execution policy.

| Source | Packages / changes |
| --- | --- |
| Chocolatey, default | `git`, `make`, `packer`, `azure-cli`, `python313`, `cygwin`, `cyg-get` |
| Cygwin setup and its configured mirror | `python312`, `python312-pip`, `python312-cryptography`, `openssh`, `git`, `make`, `gcc-core`, `gcc-g++`, `libffi-devel`, `libssl-devel`, `sshpass` |
| pip inside Cygwin | `ansible`, `pywinrm` and their dependencies |
| `--with-go` | Chocolatey `golang` |
| `--virtualbox` | Chocolatey `virtualbox`, including its drivers |
| `--vagrant` | Chocolatey `vagrant` |
| `--hyperv` | Windows Hyper-V features / Server role with management tools, and Microsoft Windows ADK Deployment Tools (`oscdimg`) if absent |

Interactive `sailwright install` can ask whether to add Hyper-V and Vagrant.
Pass `--hyperv=false --vagrant=false` to explicitly skip them. See
[Windows host dependencies](./installation.md#windows-host-dependencies).
Versions are defined in the script. Most Chocolatey packages enforce the pin,
including downgrades; sufficiently new native Git, Azure CLI and Python
installations are retained. Existing Cygwin may be stopped, removed and
reinstalled at `C:\tools\cygwin` or a versioned sibling; its HKLM Cygwin setup
registry entries are cleared during replacement. Back up existing Cygwin home
directories first. `--with-go` can remove an existing `C:\Program Files\Go`
tree before installing a clean toolchain.

Chocolatey normally stores packages, shims and logs under `C:\ProgramData\chocolatey`;
vendor installers create their own files and uninstall registry entries.
Setup adds Git's `bin` directory to the **machine** PATH, may put retained
Python and its `Scripts` directory first, and adds VirtualBox, Vagrant and
`oscdimg` directories when selected. Package installers can also edit PATH.
This is separate from the CLI installer's user PATH. In GitHub Actions,
Python's directory is also appended to `GITHUB_PATH`.
Hyper-V installs Windows services and can require a restart; the script does
not restart automatically. Other package installers may require a restart too.

### Remove Windows dependencies

Use an administrator PowerShell. Remove only components introduced for
Sailwright that are not needed by other programs. Uninstalling an upgraded
shared package does not restore its previous version. A VM snapshot taken
before installation is the reliable way to restore the entire previous state.

1. Record `choco list`, installed applications, both user and machine PATH,
   and the Cygwin root before installing. Back up Cygwin data before removal.
2. Close Cygwin shells and dependent processes. If retaining Cygwin, uninstall
   the pip packages inside its bash:

   ```bash
   python3 -m pip uninstall ansible pywinrm
   ```

   Use Cygwin setup's package selection to uninstall only the Cygwin packages
   listed above that you no longer need. Pip can leave transitive packages.
3. If removing the entire Cygwin installation instead, uninstall the
   Chocolatey wrappers first:

   ```powershell
   choco uninstall cyg-get cygwin -y
   ```

   Inspect the actual root (including versioned siblings) and delete remaining
   Cygwin files only after saving user data and confirming nothing uses them.
   If Cygwin services were added separately, stop and unregister those before
   deleting their executables. Inspect `HKLM\SOFTWARE\Cygwin\setup` and
   `HKLM\SOFTWARE\WOW6432Node\Cygwin\setup`; remove stale setup entries only
   when they refer to the removed installation. Remove its shortcuts and
   package download cache if left behind.
4. Uninstall each unneeded Chocolatey-managed package, for example:

   ```powershell
   choco uninstall make packer -y
   ```

   Repeat for `git`, `azure-cli`, `python313`, and optional `golang`,
   `virtualbox`, `vagrant` as applicable. Use Installed apps for native
   installations not managed by Chocolatey. Leave previously existing tools.
5. If ADK Deployment Tools were added, remove them through Installed apps →
   Windows Assessment and Deployment Kit → Change/Uninstall. If Hyper-V was
   enabled only for this setup, remove it through **Turn Windows features on
   or off** (client) or **Remove Roles and Features** (Server), then restart
   when requested. Preserve VMs and switches needed by other workflows.
6. Compare **System variables → Path** with the saved value. Remove only
   obsolete Git, Python/Scripts, Cygwin, Go, VirtualBox, Vagrant or ADK entries;
   restore previous ordering where appropriate. Do not overwrite unrelated
   changes made since installation. Open a new terminal.
7. Keep Chocolatey if shared. If it was installed solely for this setup,
   follow [Chocolatey's removal instructions](https://docs.chocolatey.org/en-us/choco/uninstallation/)
   after uninstalling its packages; inspect its directory, environment
   variables and PATH entry for leftovers.
8. Remove the CLI separately as described above. Inspect remaining user data.

## Linux host dependencies (apt-based systems)

[`sailwright-install-dependencies.sh`](../scripts/linux/sailwright-install-dependencies.sh)
uses `sudo` (or runs as root). It updates apt metadata, installs
`ca-certificates`, `curl`, `gpg`, and writes or replaces:

- `/usr/share/keyrings/hashicorp-archive-keyring.gpg` from the HashiCorp GPG key;
- `/etc/apt/sources.list.d/hashicorp.list` for the host distribution codename.

It then installs these packages and their dependencies:

```text
asciinema curl ffmpeg gpg libarchive-tools libvirt-clients
libvirt-daemon-system packer pipx python3 python3-venv qemu-efi-aarch64
qemu-system-arm qemu-system-x86 qemu-utils sshpass swtpm tar virt-manager
virtinst vncsnapshot xorriso xz-utils
```

It also installs `7zip`, or `p7zip-full` if `7zip` is unavailable. Packer comes
from HashiCorp; other packages use configured apt repositories. Package scripts
may enable system services such as libvirt. The Sailwright script does not
add users to groups, configure a virtual network, or edit shell profiles.

The [Ansible helper](../scripts/linux/sailwright-install-ansible.sh) uses pipx
to install/refresh `ansible` with dependency command links and inject `pywinrm`,
under `/opt/sailwright/pipx`, linking commands into `/usr/local/bin`.
With `--with-go`, the [Go helper](../scripts/linux/install-go-from-go-dev.sh)
downloads the `go.mod` version from go.dev, checks its SHA-256, replaces
`/usr/local/go`, and replaces `/usr/local/bin/go` and `/usr/local/bin/gofmt`
with symlinks. No persistent PATH edit is made.

### Remove Linux dependencies

1. Save the pre-install package list (`apt-mark showmanual`, `dpkg-query -W`),
   existing HashiCorp source/key files, and any existing Go installation.
2. Remove isolated Ansible and its command links:

   ```bash
   sudo env PIPX_HOME=/opt/sailwright/pipx PIPX_BIN_DIR=/usr/local/bin pipx uninstall ansible
   ```

   Inspect `/opt/sailwright/pipx` for remaining logs/caches and delete it only
   if no longer needed. Restore any prior command links from your backup.
3. Remove unneeded packages from the inventory above, for example:

   ```bash
   sudo apt-get remove packer
   sudo apt-get autoremove --dry-run
   ```

   Review the dry run before running `sudo apt-get autoremove`. Remove
   libvirt/QEMU packages only after preserving VMs used by other workflows.
   Purge selected packages if their system configuration is also unwanted.
4. If no other HashiCorp tools need the repository, remove its source and key:

   ```bash
   sudo rm -f /etc/apt/sources.list.d/hashicorp.list
   sudo rm -f /usr/share/keyrings/hashicorp-archive-keyring.gpg
   sudo apt-get update
   ```

   Restore the saved versions instead if these files existed beforehand.
5. If Go was added with `--with-go`, verify that the two symlinks still point
   into `/usr/local/go`, remove those links and that tree, or restore the
   previous toolchain. Do not remove a Go installation now used elsewhere.
6. Remove the CLI and optional user data separately.

## macOS host dependencies

[`sailwright-install-dependencies.sh`](../scripts/macos/sailwright-install-dependencies.sh)
requires an existing Homebrew installation. It taps `hashicorp/tap`, then
installs or upgrades `hashicorp/tap/packer`, `qemu`, `xz`, `ffmpeg`,
`vncsnapshot`, `xorriso`, `ansible`, `python@3.13`, and the `utm` cask
(`/Applications/UTM.app`). `--with-go` also installs `go`.

Formulae live under Homebrew's prefix (`brew --prefix`), with links into its
`bin` directory. The script accepts sufficiently new versions, unpins older
formulae to upgrade them, then pins satisfying formulae. `python@3.13` is
explicitly left unpinned. It does not install Homebrew, start brew services or
edit PATH/profiles. Brew/cask installers may request elevated rights for their
own installation steps; normal formula operations run as the current user.

### Remove macOS dependencies

1. Before installation, record `brew list --versions`, `brew list --cask`,
   `brew tap` and `brew list --pinned` to distinguish shared tools.
2. Unpin only formulae pinned by this setup that you plan to remove, for example
   `brew unpin packer qemu xz ffmpeg vncsnapshot xorriso ansible` (and `go`
   if selected). Restore previous pins for shared formulae you retain.
3. Run `brew uninstall` for the unneeded formulae in the inventory and
   `brew uninstall --cask utm` if UTM was added solely for this setup.
   Save UTM VMs first. Keep shared Python and other dependencies as needed.
4. Review `brew autoremove --dry-run` before `brew autoremove`; use
   `brew cleanup` to remove cached versions if desired. Untap `hashicorp/tap`
   only if no remaining formula needs it.
5. Remove the CLI and optional user data separately. Uninstalling does not
   downgrade upgraded tools; restore a backup if exact rollback is required.

## Network destinations

| Operation | Destinations |
| --- | --- |
| CLI archives, scripts, checksums | `github.com/csautter/sailwright/releases`, with GitHub release asset redirects such as `release-assets.githubusercontent.com` |
| Optional cosign verification | public Sigstore trust/transparency services used by your installed cosign version, typically `tuf-repo-cdn.sigstore.dev` and `rekor.sigstore.dev` |
| Windows packages | `community.chocolatey.org` or configured Chocolatey sources, then vendor download hosts (GitHub, HashiCorp, Microsoft, python.org, Cygwin and its mirrors; Oracle when VirtualBox is selected) |
| Windows ADK (`--hyperv`) | `go.microsoft.com` redirecting to Microsoft download services |
| Linux packages | configured distribution mirrors and `apt.releases.hashicorp.com` (including `/gpg`) |
| Python packages on Linux / Windows | configured pip index, normally `pypi.org` and `files.pythonhosted.org` |
| Optional Linux Go | `go.dev/dl/` metadata and archives, including download redirects to `dl.google.com` |
| macOS Homebrew | configured taps, Homebrew API/bottle hosts such as `formulae.brew.sh` and `ghcr.io`, GitHub and upstream formula/cask download hosts |

This is a source inventory, not a complete firewall allowlist: package recipes,
mirrors, transitive dependencies and redirects can change. Have IT inspect the
selected versions and package-manager configuration for a restricted network.
Installation does not require an Azure login or create cloud resources.

## User data

The CLI install scripts do not remove data from earlier use. Running Sailwright
can create configuration, caches, downloaded images and a working checkout
under the application data directory (historically named `dev-alchemy`):
`~/.local/share/dev-alchemy` on Linux, `~/Library/Application Support/dev-alchemy`
on macOS, and `%LOCALAPPDATA%\dev-alchemy` on Windows. Overrides may move these.
The gateway keeps separate state; see [Agent Gateway](./agent-gateway.md#disk-usage).

Destroy unwanted VMs using the CLI before removing tools, and preserve VM disks,
project files and role configuration you still need. Inspect these directories
before deleting them. CLI settings may also live in the platform configuration
directory (`~/.config/dev-alchemy` on Linux,
`~/Library/Application Support/dev-alchemy` on macOS,
and `%APPDATA%\dev-alchemy` on Windows). Deleting binaries or
uninstalling dependencies does not remove VMs, user data, Azure resources, or
changes made by provisioning roles.
