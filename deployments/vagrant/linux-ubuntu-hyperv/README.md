# Run Ubuntu with Vagrant and Hyper-V

This guide covers the Windows-host workflow for Ubuntu Hyper-V with the Sailwright CLI:

1. Build the box (`sailwright build`)
2. Create/start the VM (`sailwright create`)
3. Provision with Ansible (`sailwright provision`)

All commands are intended for PowerShell on a Windows host.

Managed Sailwright paths on Windows default to:

- App data root: `%LOCALAPPDATA%\dev-alchemy`
- Build cache: `%LOCALAPPDATA%\dev-alchemy\cache`
- Vagrant state: `%LOCALAPPDATA%\dev-alchemy\.vagrant`

Set `DEV_ALCHEMY_APP_DATA_DIR` if you want to override the default root.

## Prerequisites

- Run the dependency installer from repository root in an elevated PowerShell session:

```powershell
sailwright.exe install
```

- [Vagrant](https://www.vagrantup.com/downloads)
- [Hyper-V](https://docs.microsoft.com/en-us/virtualization/hyper-v-on-windows/quick-start/enable-hyper-v)
- [Cygwin](https://www.cygwin.com/install.html)
- [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html) via Cygwin
- [Go](https://go.dev/doc/install)

## Build Ubuntu Hyper-V Box

Run from repository root:

```powershell
# server
sailwright.exe build ubuntu --type server --arch amd64
# desktop
sailwright.exe build ubuntu --type desktop --arch amd64
```

Expected artifacts:

- `%LOCALAPPDATA%\dev-alchemy\cache\ubuntu\hyperv-ubuntu-server-amd64.box`
- `%LOCALAPPDATA%\dev-alchemy\cache\ubuntu\hyperv-ubuntu-desktop-amd64.box`

## Create/Start the VM

Set a Hyper-V switch to avoid interactive selection:

```powershell
$env:VAGRANT_HYPERV_SWITCH = "Default Switch"
```

Then create/start with the wrapper:

```powershell
# server
sailwright.exe create ubuntu --type server --arch amd64
# desktop
sailwright.exe create ubuntu --type desktop --arch amd64
```

Default guest credentials:

- Username: `packer`
- Password: `P@ssw0rd!`

The image grants `packer` password sudo only (no `NOPASSWD`). The Vagrantfile
sets `config.ssh.sudo_command` so Vagrant's privileged steps, such as setting
the hostname of `--name` instances or privileged shell provisioners you add,
authenticate with this password through a temporary `SUDO_ASKPASS` helper that
is removed after each command. If you change the guest password, update both
`config.ssh.password` and `config.ssh.sudo_command`.

## Provision with Ansible

Do not create `inventory/hyperv_ubuntu.yml` manually.
The wrapper discovers the VM IP and passes an inline inventory host to Ansible.

Run provisioning from repository root:

```powershell
# server
sailwright.exe provision ubuntu --type server --arch amd64 --check
sailwright.exe provision ubuntu --type server --arch amd64

# desktop
sailwright.exe provision ubuntu --type desktop --arch amd64 --check
sailwright.exe provision ubuntu --type desktop --arch amd64
```

Optional `.env` / environment overrides:

```dotenv
HYPERV_UBUNTU_ANSIBLE_USER=packer
HYPERV_UBUNTU_ANSIBLE_PASSWORD=P@ssw0rd!
HYPERV_UBUNTU_ANSIBLE_BECOME_PASSWORD=P@ssw0rd!
HYPERV_UBUNTU_ANSIBLE_CONNECTION=ssh
HYPERV_UBUNTU_ANSIBLE_SSH_COMMON_ARGS=-o StrictHostKeyChecking=no -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o ControlMaster=no -o ControlPersist=no
HYPERV_UBUNTU_ANSIBLE_SSH_TIMEOUT=120
HYPERV_UBUNTU_ANSIBLE_SSH_RETRIES=3
```

Optional Cygwin shell override:

```powershell
$env:CYGWIN_BASH_PATH = "C:\tools\cygwin\bin\bash.exe"
# or
$env:CYGWIN_TERMINAL_PATH = "C:\tools\cygwin\bin\mintty.exe"
```

## Manual Vagrant Commands (Optional)

If you want to run Vagrant directly:

```powershell
$AppDataDir = if ($env:DEV_ALCHEMY_APP_DATA_DIR) { $env:DEV_ALCHEMY_APP_DATA_DIR } else { Join-Path $env:LOCALAPPDATA "dev-alchemy" }
$CacheDir = Join-Path $AppDataDir "cache"
$VagrantRoot = Join-Path $AppDataDir ".vagrant"
$type = "server" # or "desktop"
$env:VAGRANT_BOX_NAME = "linux-ubuntu-$type-packer"
$env:VAGRANT_VM_NAME = "linux-ubuntu-$type-packer"
$env:VAGRANT_DOTFILE_PATH = Join-Path $VagrantRoot $env:VAGRANT_VM_NAME
vagrant box add $env:VAGRANT_BOX_NAME (Join-Path $CacheDir "ubuntu\hyperv-ubuntu-$type-amd64.box") --provider hyperv --force
cd deployments\vagrant\linux-ubuntu-hyperv
vagrant up --provider hyperv
cd ..\..\..
```

## Destroy and Cleanup

```powershell
$AppDataDir = if ($env:DEV_ALCHEMY_APP_DATA_DIR) { $env:DEV_ALCHEMY_APP_DATA_DIR } else { Join-Path $env:LOCALAPPDATA "dev-alchemy" }
$VagrantRoot = Join-Path $AppDataDir ".vagrant"
$env:VAGRANT_DOTFILE_PATH = Join-Path $VagrantRoot "linux-ubuntu-server-packer"
cd deployments\vagrant\linux-ubuntu-hyperv
vagrant destroy -f
vagrant box remove linux-ubuntu-server-packer --provider hyperv
$env:VAGRANT_DOTFILE_PATH = Join-Path $VagrantRoot "linux-ubuntu-desktop-packer"
vagrant box remove linux-ubuntu-desktop-packer --provider hyperv
cd ..\..\..
```
