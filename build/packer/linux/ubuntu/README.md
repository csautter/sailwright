# Ubuntu Packer Templates

This directory contains Packer templates used by the Sailwright CLI to build Ubuntu images.

## Build Ubuntu on Windows Hosts (Hyper-V)

Use the CLI from repository root:

```powershell
sailwright.exe install

# build ubuntu server (Hyper-V)
sailwright.exe build ubuntu --type server --arch amd64
# build ubuntu desktop (Hyper-V)
sailwright.exe build ubuntu --type desktop --arch amd64
```

The Hyper-V template is [linux-ubuntu-hyperv.pkr.hcl](linux-ubuntu-hyperv.pkr.hcl).
Both server and desktop builds use the Ubuntu **live-server** ISO for unattended installation.
Hyper-V cloud-init input is split by type:
- `cloud-init/hyperv-server/meta-data` + `cloud-init/hyperv-server/user-data`
- `cloud-init/hyperv-desktop/meta-data` + `cloud-init/hyperv-desktop/user-data`
Both Hyper-V variants use cloud-init apt offline mode (`fallback: offline-install`, `geoip: false`) like the QEMU variants.

Manual Packer usage:

```powershell
$AppDataDir = if ($env:DEV_ALCHEMY_APP_DATA_DIR) { $env:DEV_ALCHEMY_APP_DATA_DIR } else { Join-Path $env:LOCALAPPDATA "dev-alchemy" }
$CacheDir = Join-Path $AppDataDir "cache"
$env:DEV_ALCHEMY_CACHE_DIR = $CacheDir
$env:DEV_ALCHEMY_PACKER_CACHE_DIR = Join-Path $AppDataDir "packer_cache"
$isoPath = Join-Path $CacheDir "linux\ubuntu-24.04.5-live-server-amd64.iso"

packer init build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl

# server
packer build -var "cache_dir=$CacheDir" -var "ubuntu_type=server" -var "iso_url=$isoPath" build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl

# desktop
packer build -var "cache_dir=$CacheDir" -var "ubuntu_type=desktop" -var "iso_url=$isoPath" build/packer/linux/ubuntu/linux-ubuntu-hyperv.pkr.hcl
```

Output boxes:

- `%LOCALAPPDATA%\dev-alchemy\cache\ubuntu\hyperv-ubuntu-server-amd64.box`
- `%LOCALAPPDATA%\dev-alchemy\cache\ubuntu\hyperv-ubuntu-desktop-amd64.box`

## Build Ubuntu on macOS Hosts (UTM/QEMU)

Use the CLI from repository root:

```bash
# amd64 or arm64
arch=amd64
sailwright install # --with-go also bootstraps the Go toolchain on macOS
sailwright build list # shows whether the Ubuntu build artifacts are already present in the local cache
sailwright build ubuntu --type server --arch $arch
sailwright build ubuntu --type desktop --arch $arch
```

Manual script usage:

```bash
export DEV_ALCHEMY_APP_DATA_DIR="${DEV_ALCHEMY_APP_DATA_DIR:-$HOME/Library/Application Support/dev-alchemy}"
build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh --project-root "$PWD" --arch amd64 --ubuntu-type server
build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh --project-root "$PWD" --arch amd64 --ubuntu-type desktop
```

The macOS wrapper uses the shared QEMU template,
[linux-ubuntu-qemu.pkr.hcl](linux-ubuntu-qemu.pkr.hcl), with macOS-specific
display and acceleration settings.

## Build Ubuntu on Linux Hosts (QEMU)

Install host dependencies first:

```bash
sailwright install
```

This runs
[scripts/linux/sailwright-install-dependencies.sh](../../../../scripts/linux/sailwright-install-dependencies.sh)
to install the Ubuntu/Debian packages needed by the Linux QEMU workflow.

Use the CLI from repository root:

```bash
# amd64 or arm64
arch=amd64
sailwright build ubuntu --type server --arch "$arch"
sailwright build ubuntu --type desktop --arch "$arch"
sailwright create ubuntu --type server --arch "$arch"
sailwright start ubuntu --type server --arch "$arch"
```

Manual script usage:

```bash
export DEV_ALCHEMY_APP_DATA_DIR="${DEV_ALCHEMY_APP_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy}"
build/packer/linux/ubuntu/linux-ubuntu-on-linux.sh --project-root "$PWD" --arch amd64 --ubuntu-type server
build/packer/linux/ubuntu/linux-ubuntu-on-linux.sh --project-root "$PWD" --arch amd64 --ubuntu-type desktop
```

The Linux wrapper also uses the shared QEMU template,
[linux-ubuntu-qemu.pkr.hcl](linux-ubuntu-qemu.pkr.hcl), after probing whether
KVM acceleration is usable on the host.

QEMU autoinstall uses the attached `cidata` seed plus the Packer boot command's
`autoinstall ds=nocloud` kernel argument. The QEMU cloud-init seed intentionally
leaves networking to Subiquity's default DHCP handling for `eth*`/`en*`
interfaces. During ARM64 software emulation the installer can take longer than
systemd's default D-Bus method timeout while applying netplan, so the QEMU seed
extends the live installer's `busctl` timeout before Subiquity applies network
configuration.

The Linux `create`/`start`/`stop`/`destroy` flow uses libvirt so the VM appears
in `virt-manager`.

Linux libvirt runtime prefers native-architecture guests for `create all` and
`start all`. Cross-architecture guests are available when selected explicitly,
but they are marked unstable because QEMU TCG emulation is slow and less tested.

Ubuntu QEMU images include `qemu-guest-agent` and `spice-vdagent`, and the
libvirt domain enables the SPICE agent channel so `virt-manager` can provide
better clipboard, pointer, and dynamic display resize integration for desktop
guests. The Linux libvirt runtime uses a `virtio` video device for Ubuntu
guests.

- Default libvirt connection: `qemu:///system`
- Default managed disk directory for that connection: `/var/tmp/dev-alchemy/libvirt/images`
- Managed disk directories are created with mode `0750`; grant system libvirt
  access explicitly with a storage pool, group ownership/ACLs, or a custom
  `DEV_ALCHEMY_LIBVIRT_IMAGE_DIR` when needed.
- Optional override for rootless libvirt user-session VMs: `DEV_ALCHEMY_LIBVIRT_URI=qemu:///session`
- Optional managed disk directory override: `DEV_ALCHEMY_LIBVIRT_IMAGE_DIR=/path/to/images`
