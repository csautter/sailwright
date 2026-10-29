# Windows Packer Template

## Build Windows on Windows Hosts

This directory contains the Packer templates for building Windows 11 images on Windows, macOS, and Linux hosts.

### Prerequisites

- [Packer](https://www.packer.io/downloads) installed
- Windows, macOS, or Linux host with a compatible virtualization backend
- For repository-managed host dependencies, run `sailwright install` on macOS/Linux or `sailwright.exe install` on Windows from repo root before building.
- The automatic Windows 11 ISO download needs Python 3.10 or newer. macOS ships Python 3.9 as `/usr/bin/python3`; `sailwright install` adds Homebrew `python@3.13`. See [scripts/macos/README.md](../../../scripts/macos/README.md).

### Usage

For manual builds on Windows, use the current Hyper-V or VirtualBox templates and point them at the managed Sailwright cache.

```powershell
$AppDataDir = if ($env:DEV_ALCHEMY_APP_DATA_DIR) { $env:DEV_ALCHEMY_APP_DATA_DIR } else { Join-Path $env:LOCALAPPDATA "dev-alchemy" }
$CacheDir = Join-Path $AppDataDir "cache"
$env:DEV_ALCHEMY_CACHE_DIR = $CacheDir
$env:DEV_ALCHEMY_PACKER_CACHE_DIR = Join-Path $AppDataDir "packer_cache"

# Find newest ISO file in the managed cache
$isoPath = Get-ChildItem -Path (Join-Path $CacheDir "windows11\iso") -Filter "Win11_*.iso" | Sort-Object LastWriteTime -Descending | Select-Object -First 1 | Select-Object -ExpandProperty FullName
Write-Host "Using ISO: $isoPath"
```

To build the Windows image, run:

```powershell
# Hyper-V
packer init build/packer/windows/windows11-on-windows-hyperv.pkr.hcl
packer build -var "cache_dir=$CacheDir" -var "iso_url=$isoPath" build/packer/windows/windows11-on-windows-hyperv.pkr.hcl

# VirtualBox
packer init build/packer/windows/windows11-on-windows-virtualbox.pkr.hcl
packer build -var "cache_dir=$CacheDir" -var "iso_url=$isoPath" build/packer/windows/windows11-on-windows-virtualbox.pkr.hcl
```

You can reduce build time by disabling compression in the `post-processor "vagrant"` block of the relevant template and setting `compression_level = 0`.
Default for packer is `6`.
[Compression Level Reference](https://developer.hashicorp.com/packer/docs/post-processors/compress#compression_level)

### Output

## Build Windows on macOS or Linux Hosts

The windows VM build process is fully automated and includes installation of:

- Download Windows iso
- Unattended Windows installation
- Qemu Guest Additions
- WinRM enabled
- SSH server installed
- UTM VM on macOS or libvirt VM on Linux

### run build and create

After running following commands, the Windows 11 VM will be available in UTM on macOS or libvirt on Linux.
Every command may take a while to finish. If something goes wrong, please check the logs and retry.
The process is idempotent, so you can re-run commands without issues.

```bash
arch=arm64 # or amd64
sailwright install
sailwright build windows11 --arch $arch
sailwright create windows11 --arch $arch
```

Start the VM in UTM, virt-manager, or via the CLI and provision it from repository root with the unified wrapper:

```bash
sailwright start windows11 --arch $arch
sailwright provision windows11 --arch $arch --check
sailwright provision windows11 --arch $arch
```

WinRM credentials default to the `Administrator` / `P@ssw0rd!` account from the answer files. Override them in project-root `.env` or the process environment if you changed it:

```dotenv
UTM_WINDOWS_ANSIBLE_USER=Administrator
UTM_WINDOWS_ANSIBLE_PASSWORD=P@ssw0rd!
# On Linux/libvirt use:
LIBVIRT_WINDOWS_ANSIBLE_USER=Administrator
LIBVIRT_WINDOWS_ANSIBLE_PASSWORD=P@ssw0rd!
```

### ARM64 on macOS

Windows 11 ARM64 uses the same pinned `virt-10.0` machine type on macOS and
the same pinned Debian AAVMF firmware as the Ubuntu ARM64 builds. Without
the pin, Homebrew QEMU 11.x hangs on the TianoCore screen before the Windows
boot loader starts. See
[ARM64 on macOS: pinned machine type and firmware](../linux/ubuntu/README.md#arm64-on-macos-pinned-machine-type-and-firmware).

### Security Note

Current Windows templates keep WinRM provisioning reachable even when Windows reclassifies the NIC as `Public`. They do that by enabling WinRM Basic over HTTP, allowing unencrypted WSMan traffic, and creating an inbound firewall rule for TCP `5985` with `Profile Any` and `RemoteAddress Any`.

That choice broadens the reachable attack surface compared with private-profile-only access. During unattended setup, the built-in `Administrator` credential is also configured in the answer file, so these images should only be built and booted on trusted, isolated networks.

A safer provisioning approach is planned for future releases. Until then, treat the current WinRM path as a compatibility tradeoff rather than a hardened default.
