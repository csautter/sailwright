# Run Ansible Windows with Vagrant and Hyper-V

This guide will help you set up and run Ansible playbooks on a Windows VM using Vagrant with Hyper-V as the provider.
All commands are meant to be run in a powershell terminal on a Windows host machine.

For manually prepared Windows targets outside this managed VM flow, see
[Windows Ansible Access](../../../docs/windows-ansible-access.md).

Managed Sailwright paths on Windows default to:

- App data root: `%LOCALAPPDATA%\dev-alchemy`
- Build cache: `%LOCALAPPDATA%\dev-alchemy\cache`
- Vagrant state: `%LOCALAPPDATA%\dev-alchemy\.vagrant`

Set `DEV_ALCHEMY_APP_DATA_DIR` if you want to override the default root.

## Prerequisites

Run the dependency installer from repository root in an elevated PowerShell session:

```powershell
sailwright.exe install
```

Ensure you have the following installed:

- [Vagrant](https://www.vagrantup.com/downloads)
- [Hyper-V](https://docs.microsoft.com/en-us/virtualization/hyper-v-on-windows/quick-start/enable-hyper-v)
- [Cygwin](https://www.cygwin.com/install.html)
- [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html) (via Cygwin)
- [Go](https://go.dev/doc/install)

## Adding the Vagrant Box

Load the Vagrant box and start the VM using Hyper-V as the provider.
The box artifact is expected at `%LOCALAPPDATA%\dev-alchemy\cache\windows11\hyperv-windows11-amd64.box`.

Set `VAGRANT_HYPERV_SWITCH` to avoid Hyper-V bridge selection prompts during `vagrant up`:

```powershell
$env:VAGRANT_HYPERV_SWITCH = "Default Switch"
```

Then add the box and boot the VM:

```powershell
$AppDataDir = if ($env:DEV_ALCHEMY_APP_DATA_DIR) { $env:DEV_ALCHEMY_APP_DATA_DIR } else { Join-Path $env:LOCALAPPDATA "dev-alchemy" }
$CacheDir = Join-Path $AppDataDir "cache"
$VagrantRoot = Join-Path $AppDataDir ".vagrant"
$env:VAGRANT_DOTFILE_PATH = Join-Path $VagrantRoot "win11-packer"
vagrant box add win11-packer (Join-Path $CacheDir "windows11\hyperv-windows11-amd64.box") --provider hyperv
vagrant up --provider hyperv
```

After the VM is up, you can connect to it using Hyper-V Manager or via RDP. The default credentials are:

- Username: `Administrator`
- Password: `P@ssw0rd!`

## Optional: Determine the IP Address of the Vagrant Box

The provisioning wrapper discovers the VM IP automatically, but you can inspect it manually:

```powershell
vagrant winrm -c "ipconfig"
```

Alternatively, you can use PowerShell to extract the IP address directly:

```powershell
$vagrant_ip = (vagrant winrm -c "ipconfig" | Select-String -Pattern 'IPv4 Address.*: (\d{1,3}\.){3}\d{1,3}' | ForEach-Object { $_.Matches[0].Value.Split(':')[1].Trim() })
Write-Output "Vagrant Box IP Address: $vagrant_ip"
```

## Configure WinRM Credentials for Provisioning

Do not create `inventory/hyperv_windows_winrm.yml`. Hyper-V provisioning now passes the discovered host directly to Ansible and reads credentials from environment variables.

Credentials default to the `Administrator` / `P@ssw0rd!` account baked into the Windows image. Override them in a project-root `.env` file (or process environment) if you changed it:

```dotenv
# Optional (defaults shown):
HYPERV_WINDOWS_ANSIBLE_USER=Administrator
HYPERV_WINDOWS_ANSIBLE_PASSWORD=P@ssw0rd!
HYPERV_WINDOWS_ANSIBLE_CONNECTION=winrm
HYPERV_WINDOWS_ANSIBLE_WINRM_TRANSPORT=basic
HYPERV_WINDOWS_ANSIBLE_PORT=5985
```

Optional shell path overrides for Cygwin execution:

```powershell
$env:CYGWIN_BASH_PATH = "C:\tools\cygwin\bin\bash.exe"
# or set your Cygwin terminal path:
$env:CYGWIN_TERMINAL_PATH = "C:\tools\cygwin\bin\mintty.exe"
```

Path resolution precedence used by provisioning:

1. `CYGWIN_BASH_PATH` (highest priority)
2. `CYGWIN_TERMINAL_PATH` (used only when `CYGWIN_BASH_PATH` is unset)
3. Auto-detect `C:\tools\cygwin\bin\bash.exe`
4. Auto-detect `C:\cygwin64\bin\bash.exe`

If `CYGWIN_TERMINAL_PATH` points to `mintty.exe`, provisioning resolves it to the sibling `bash.exe`.

## Run Provisioning

After installing host dependencies, run provisioning from the repository root. The wrapper resolves IP address via `vagrant winrm -c ipconfig` and runs `ansible-playbook` through Cygwin.

```powershell
sailwright.exe provision windows11 --arch amd64 --check
sailwright.exe provision windows11 --arch amd64
```

## Destroying the Vagrant Box

When you are done, you can destroy the Vagrant box and remove the box from your system:

```powershell
vagrant destroy
vagrant box remove win11-packer --provider hyperv
```
