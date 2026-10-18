# Testing Workflows

This guide collects the platform-specific testing workflows that used to live in the main `README.md`.
Use the cross-platform matrix in the root [README](../README.md) for a quick support overview, then use the sections below for concrete commands.

## Unified VM Workflow

Most VM-backed test flows follow the same lifecycle from the repository root:

```bash
sailwright build <osname> [--type <type>] [--arch <arch>]
sailwright create <osname> [--type <type>] [--arch <arch>]
sailwright start <osname> [--type <type>] [--arch <arch>]
sailwright provision <osname> [--type <type>] [--arch <arch>] --check
sailwright provision <osname> [--type <type>] [--arch <arch>]
sailwright stop <osname> [--type <type>] [--arch <arch>]
sailwright destroy <osname> [--type <type>] [--arch <arch>]
```

- `build` creates or refreshes the reusable VM artifact.
- `create` creates the managed VM target from that artifact.
- `start` starts an existing created VM when it is stopped.
- `provision` runs the Ansible workflow against the running target.
- `stop` shuts the VM down without deleting it.
- `destroy` removes the managed VM target.

Depending on the backend, the initial boot may happen during `create` or require a small host-specific step. After a VM has been created, use `start` whenever you want to boot it again.

Use the `list` subcommands to see what your current host supports:

```bash
sailwright build list
sailwright create list
sailwright start list
sailwright provision list
sailwright stop list
sailwright destroy list
```

Use `--help` on the root command or any subcommand to inspect supported flags and usage details:

```bash
sailwright --help
sailwright create --help
sailwright start --help
sailwright provision --help
```

For the fully automated, single-command, recorded end-to-end run of the whole
lifecycle (`install -> build -> create -> start -> provision --check ->
provision -> (stop -> destroy)`), see
[Automated Quick-Start](quickstart-automation.md).

## Deterministic E2E Scenarios

The repeated user cycles are covered by deterministic scenarios in
[`test/e2e/scenarios`](../test/e2e/scenarios). Each scenario is a YAML file
that runs as a subtest of `go test -tags e2e ./test/e2e`:

| Scenario | Needs | Covers |
| --- | --- | --- |
| `smoke` | nothing | help output and `list --json` for commands that need no hypervisor |
| `vm-lifecycle` | `SAILWRIGHT_E2E_TARGET` | pull or build, create, start, provision `--check`, provision, idempotent re-run, stop, destroy (always) |
| `destroy-idempotent` | `SAILWRIGHT_E2E_TARGET` | destroying twice succeeds |
| `local-check` | host changes allowed | `provision local --check` with the default playbook |

Steps run through an executor:

- **direct** runs the Sailwright binary itself. Use it on disposable CI runners
  or as a human on the host.
- **gateway** runs every step through [`sailwright-gateway`](./agent-gateway.md),
  so agents in a devcontainer get the same tests and the gateway policy still
  applies. Steps outside the safe set, such as `local-check`, wait for approval
  on the gateway terminal.

```bash
# direct, on the host
make test-e2e-smoke
make test-e2e E2E_RUN='TestScenarios/vm-lifecycle' E2E_TARGET=ubuntu/server/amd64

# gateway, from the devcontainer
SAILWRIGHT_E2E_EXECUTOR=gateway SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64 \
  go test -tags e2e ./test/e2e -run 'TestScenarios/(smoke|vm-lifecycle)' -count=1 -timeout 6h -v
```

| Variable | Meaning |
| --- | --- |
| `SAILWRIGHT_E2E_EXECUTOR` | `direct` (default) or `gateway` |
| `SAILWRIGHT_E2E_BIN` | Sailwright binary for the direct executor |
| `SAILWRIGHT_E2E_TARGET` | `os/type/arch`, e.g. `ubuntu/server/amd64` or `windows11//amd64` |
| `SAILWRIGHT_E2E_PULL_REF` | pull this OCI artifact instead of building in `vm-lifecycle` |
| `SAILWRIGHT_E2E_ALLOW_HOST_CHANGES` | `1` lets the direct executor run host-changing steps |
| `SAILWRIGHT_E2E_OUT` | report directory (default `test/e2e/out`) |
| `SAILWRIGHT_GATEWAY_URL`, `SAILWRIGHT_GATEWAY_TOKEN` | gateway endpoint and token |

Each run writes `<scenario>.json` and `<scenario>.md` reports with the host,
binary hash and per-step results. For JUnit output, run the same command with
`gotestsum --junitfile`.

### Writing scenarios

```yaml
name: my-scenario            # must match the file name
description: What this checks.
requires: [target]           # target and/or host-changes
steps:
  - name: provision in check mode
    op: provision            # build create start stop destroy provision pull provision_local list cli
    with: {check: true}
    expect:
      exit_code: 0           # default 0
      output_regex: "PLAY RECAP"
      recap: {failed: 0, changed: 0}
      state: {command: start, field: state, regex: "^running$"}
      max_duration: 30m
  - name: direct-only call
    op: cli
    only: direct             # or gateway
    when_env: SAILWRIGHT_E2E_SOMETHING
    with: {args: ["build", "list", "--json"]}
always:
  - name: cleanup
    op: destroy
```

After a failed step the remaining steps are skipped; `always` steps still run.
Only `${SAILWRIGHT_E2E_*}` variables are expanded in `with` values. Unknown
fields, operations and recap keys are rejected when the scenario loads.

## OCI Build Artifact Registry Workflow

Final build artifacts can be pushed to, and pulled from, an OCI registry. The
commands use the same target vocabulary as the VM workflow and a Docker-like
reference argument:

```bash
sailwright push list
sailwright pull list

sailwright push localhost:5000/sailwright/ubuntu-server-amd64:qemu \
  --plain-http \
  --os ubuntu \
  --type server \
  --arch amd64 \
  --host-os linux

sailwright pull localhost:5000/sailwright/ubuntu-server-amd64:qemu \
  --plain-http \
  --os ubuntu \
  --type server \
  --arch amd64 \
  --host-os linux
```

The OCI client reads Docker credentials by default, so `docker login` works for
authenticated registries. Use `--username`, `--password-stdin`, or
`--access-token` when you want command-specific credentials.

The Linux GitHub Actions build workflow publishes completed Ubuntu artifacts to
GitHub Container Registry on pushes to `main` and on manual `workflow_dispatch`
runs started from `main`. Artifacts are stored under
`ghcr.io/<owner>/ubuntu-24` with tags using the
`<type>-<arch>-<hostos>-build` schema, for example:

```bash
ghcr.io/csautter/ubuntu-24:server-amd64-linux-build
ghcr.io/csautter/ubuntu-24:desktop-arm64-linux-build
```

Windows VM build artifacts are intentionally not published.

For HTTPS registries with an internal or self-signed certificate, prefer adding
the registry CA with `--ca-file /path/to/ca.pem`. For short-lived testing
against a registry you already trust, `--insecure-skip-tls-verify` disables TLS
certificate verification.

For a local zot registry, use `--plain-http`. To run the opt-in integration test
against that registry:

```bash
DEV_ALCHEMY_OCI_INTEGRATION_REF=localhost:5000/sailwright/integration:oci \
DEV_ALCHEMY_OCI_INTEGRATION_PLAIN_HTTP=true \
go test ./pkg/oci -run TestOCIRegistryPushPullIntegration -count=1
```

The pushed artifact is an ordinary OCI artifact, so it can also be inspected
with ORAS:

```bash
oras manifest fetch --plain-http localhost:5000/sailwright/ubuntu-server-amd64:qemu
```

## Local Host Provisioning

Use the shared local wrapper when you want to apply the playbook to the current
machine instead of a managed VM:

```bash
sailwright provision local --check
sailwright provision local --proto ssh --check
sailwright provision local --playbook ./playbooks/setup.yml --check
sailwright provision local
```

For platform defaults, configured playbook resolution, advanced flags, and
Windows cleanup behavior, use [Local Provisioning](./local-provisioning.md).

## System-Agnostic Docker Workflow

### Ubuntu role tests on Linux, WSL, Windows, or macOS

Use the provided Docker Compose setup to run the Ubuntu-focused Ansible playbook inside a container:

```bash
docker compose -f deployments/docker-compose/ansible/docker-compose.yml up
```

Clean up afterwards with:

```bash
docker compose -f deployments/docker-compose/ansible/docker-compose.yml down
```

## Linux Host Workflows

### Ubuntu on Linux with QEMU/KVM and virt-manager

Install host dependencies first:

```bash
sailwright install
```

`sailwright install` installs the libvirt and QEMU host packages, but some
distributions still require one manual host step before the managed VM can
boot: enable libvirt's `default` network, or grant ACL/group access to the
system libvirt image directory if the daemon runs guests as a different user.

Build and create the Ubuntu VM:

```bash
arch=amd64 # or arm64
type=desktop # or server
sailwright build ubuntu --arch "$arch" --type "$type"
sailwright create ubuntu --arch "$arch" --type "$type"
```

By default the Linux create/start/stop/destroy workflow uses the libvirt system
connection (`qemu:///system`) so the VM attaches to libvirt's standard NAT
network and gets outbound internet access in the common case. Managed QCOW2
disks default to `/var/tmp/dev-alchemy/libvirt/images`, which avoids requiring a
pre-created root-owned image directory. Sailwright creates managed image
directories with mode `0750`; if your system libvirt daemon runs guests as a
different user, grant access explicitly with a libvirt storage pool, group
ownership/ACLs on `DEV_ALCHEMY_LIBVIRT_IMAGE_DIR`, or use the session connection
below.

When a VM uses a named libvirt network, Sailwright preflights it before defining or
starting the domain. With the default system URI this checks:

```bash
virsh --connect qemu:///system net-info default
```

If the `default` network exists but is inactive, enable it with:

```bash
sudo virsh --connect qemu:///system net-start default
sudo virsh --connect qemu:///system net-autostart default
```

If you prefer the rootless libvirt user session instead, set:

```bash
export DEV_ALCHEMY_LIBVIRT_URI=qemu:///session
# Optional when you want a custom storage location for the session connection:
export DEV_ALCHEMY_LIBVIRT_IMAGE_DIR="$HOME/.local/share/dev-alchemy/libvirt/images"
```

The built-in session path uses libvirt user-mode networking, so it does not
require the `default` NAT network. If a named network is configured for a session
URI later, the same preflight is run against `qemu:///session`.

If you prefer the traditional system libvirt image location instead, set:

```bash
export DEV_ALCHEMY_LIBVIRT_URI=qemu:///system
export DEV_ALCHEMY_LIBVIRT_IMAGE_DIR=/var/lib/libvirt/images/dev-alchemy
```

You can then boot the created VM either from `virt-manager` or from the CLI:

```bash
sailwright start ubuntu --arch "$arch" --type "$type"
sailwright provision ubuntu --arch "$arch" --type "$type" --check
sailwright provision ubuntu --arch "$arch" --type "$type"
sailwright stop ubuntu --arch "$arch" --type "$type"
sailwright destroy ubuntu --arch "$arch" --type "$type"
```

The provision wrapper discovers the libvirt guest IP with `virsh domifaddr`
and runs `ansible-playbook` with an inline SSH inventory target. Optional
Ubuntu provisioning overrides can be set in `.env` or the process environment
using `LIBVIRT_UBUNTU_ANSIBLE_*`:

```dotenv
LIBVIRT_UBUNTU_ANSIBLE_USER=packer
LIBVIRT_UBUNTU_ANSIBLE_PASSWORD=P@ssw0rd!
LIBVIRT_UBUNTU_ANSIBLE_BECOME_PASSWORD=P@ssw0rd!
# Optional (defaults shown):
LIBVIRT_UBUNTU_ANSIBLE_CONNECTION=ssh
LIBVIRT_UBUNTU_ANSIBLE_SSH_COMMON_ARGS=-o StrictHostKeyChecking=no -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o ControlMaster=no -o ControlPersist=no
LIBVIRT_UBUNTU_ANSIBLE_SSH_TIMEOUT=120
LIBVIRT_UBUNTU_ANSIBLE_SSH_RETRIES=3
```

Related guide:

- [Ubuntu Packer README](../build/packer/linux/ubuntu/README.md)

### Windows on Linux with QEMU/KVM and virt-manager

Install host dependencies first:

```bash
sailwright install
```

Build and create the Windows 11 VM:

```bash
arch=amd64 # or arm64
sailwright build windows11 --arch "$arch" --headless
sailwright create windows11 --arch "$arch"
```

You can then boot the created VM either from `virt-manager` or from the CLI:

```bash
sailwright start windows11 --arch "$arch"
```

The lifecycle uses the same libvirt defaults as Ubuntu on Linux: `qemu:///system`
and managed disks under `/var/tmp/dev-alchemy/libvirt/images` unless
`DEV_ALCHEMY_LIBVIRT_URI` or `DEV_ALCHEMY_LIBVIRT_IMAGE_DIR` is set.

If `/dev/kvm` is unavailable or unreliable, set
`DEV_ALCHEMY_QEMU_FORCE_SOFTWARE_EMULATION=1` to bypass KVM probing and force
QEMU TCG software emulation.

Set WinRM credentials in project-root `.env` or the process environment:

```dotenv
LIBVIRT_WINDOWS_ANSIBLE_USER=Administrator
LIBVIRT_WINDOWS_ANSIBLE_PASSWORD=your-secure-password
# Optional (defaults shown):
LIBVIRT_WINDOWS_ANSIBLE_CONNECTION=winrm
LIBVIRT_WINDOWS_ANSIBLE_WINRM_TRANSPORT=basic
LIBVIRT_WINDOWS_ANSIBLE_PORT=5985
```

Provision it from the repository root:

```bash
sailwright provision windows11 --arch "$arch" --check
sailwright provision windows11 --arch "$arch"
sailwright stop windows11 --arch "$arch"
sailwright destroy windows11 --arch "$arch"
```

The provision wrapper discovers the libvirt guest IP with `virsh domifaddr`
and runs `ansible-playbook` with an inline WinRM inventory target.

Related guide:

- [Windows Packer README](../build/packer/windows/README.md)

## Windows Host Workflows

### Ubuntu on Windows with Hyper-V

Install host dependencies first:

```powershell
sailwright.exe install
```

Build the Ubuntu artifact:

```powershell
# server
sailwright.exe build ubuntu --type server --arch amd64
# desktop
sailwright.exe build ubuntu --type desktop --arch amd64
```

Create the VM:

```powershell
$env:VAGRANT_HYPERV_SWITCH = "Default Switch"
sailwright.exe create ubuntu --type server --arch amd64
# or desktop
sailwright.exe create ubuntu --type desktop --arch amd64
```

Provision it:

```powershell
sailwright.exe provision ubuntu --type server --arch amd64 --check
sailwright.exe provision ubuntu --type server --arch amd64
```

The command discovers the VM IP automatically and runs Ansible through the Windows/Cygwin wrapper.
Optional Ubuntu provisioning overrides can be set in `.env` using `HYPERV_UBUNTU_ANSIBLE_*`.

Related guides:

- [Ubuntu Packer README](../build/packer/linux/ubuntu/README.md)
- [Ubuntu Hyper-V deployment README](../deployments/vagrant/linux-ubuntu-hyperv/README.md)

### Windows on Windows with Docker Desktop

Use Docker Desktop with Windows containers enabled:

```bash
docker compose -f deployments/docker-compose/ansible-windows/docker-compose.yml up
```

Clean up afterwards with:

```bash
docker compose -f deployments/docker-compose/ansible-windows/docker-compose.yml down
```

More details:

- [Docker Windows Ansible README](../deployments/docker-compose/ansible-windows/README.md)

### Windows on Windows with Hyper-V

Install host dependencies first:

```powershell
sailwright.exe install
```

You will need a Windows ISO for the build. You can download one manually from Microsoft or use:

- [download_win_11.ps1](../scripts/windows/download_win_11.ps1)

Build details live here:

- [Windows Packer README](../build/packer/windows/README.md)
- [Windows Hyper-V deployment README](../deployments/vagrant/ansible-windows/README.md)

After the VM is running, provision it from the repository root:

```powershell
sailwright.exe provision windows11 --arch amd64 --check
sailwright.exe provision windows11 --arch amd64
```

Set WinRM credentials in project-root `.env` or the process environment:

```dotenv
HYPERV_WINDOWS_ANSIBLE_USER=Administrator
HYPERV_WINDOWS_ANSIBLE_PASSWORD=your-secure-password
# Optional (defaults shown):
HYPERV_WINDOWS_ANSIBLE_CONNECTION=winrm
HYPERV_WINDOWS_ANSIBLE_WINRM_TRANSPORT=basic
HYPERV_WINDOWS_ANSIBLE_PORT=5985
```

Optional shell path overrides for the Windows/Cygwin wrapper:

```powershell
$env:CYGWIN_BASH_PATH = "C:\tools\cygwin\bin\bash.exe"
# or, if you prefer setting the Cygwin terminal path:
$env:CYGWIN_TERMINAL_PATH = "C:\tools\cygwin\bin\mintty.exe"
```

Path resolution precedence for provisioning:

1. `CYGWIN_BASH_PATH`
2. `CYGWIN_TERMINAL_PATH` when `CYGWIN_BASH_PATH` is unset
3. Auto-detect `C:\tools\cygwin\bin\bash.exe`
4. Auto-detect `C:\cygwin64\bin\bash.exe`

If `CYGWIN_TERMINAL_PATH` points to `mintty.exe`, provisioning resolves it to the sibling `bash.exe`.

## macOS Host Workflows

### macOS on macOS with Tart

Use the provided script:

```bash
./scripts/macos/test-ansible-macos.sh
```

The script runs the Ansible playbook against a temporary Tart VM.
Tart project:

- https://github.com/cirruslabs/tart

By default, Sailwright uses the Tart image's development credentials for Ansible access (`admin` / `admin`).
Override them in `.env` if needed:

```bash
TART_MACOS_ANSIBLE_USER=admin
TART_MACOS_ANSIBLE_PASSWORD=admin
```

`TART_MACOS_ANSIBLE_BECOME_PASSWORD` defaults to `TART_MACOS_ANSIBLE_PASSWORD` when unset.

Clean up afterwards with:

```bash
tart delete sequoia-base
```

### Windows on macOS with UTM

Install host dependencies first:

```bash
sailwright install
```

Build and create the Windows 11 VM:

```bash
# arm64 requires sudo to create a custom .iso file for automated installation.
# sudo rights are evaluated at runtime, so you can run the build command without sudo and it will ask for sudo rights only if needed.
arch=arm64 # or amd64
# sudo sailwright build windows11 --arch $arch --headless
sailwright build windows11 --arch $arch --headless
# `--headless` applies to `build`, not `create`.
sailwright create windows11 --arch $arch
```

Open UTM and start the created VM.

Set WinRM credentials in project-root `.env` or the process environment:

```dotenv
UTM_WINDOWS_ANSIBLE_USER=Administrator
UTM_WINDOWS_ANSIBLE_PASSWORD=your-secure-password
# Optional (defaults shown):
UTM_WINDOWS_ANSIBLE_CONNECTION=winrm
UTM_WINDOWS_ANSIBLE_WINRM_TRANSPORT=basic
UTM_WINDOWS_ANSIBLE_PORT=5985
```

Provision it from the repository root:

```bash
sailwright provision windows11 --arch $arch --check
sailwright provision windows11 --arch $arch
```

The wrapper discovers the VM IP automatically from the generated UTM config and `arp -a`, then runs `ansible-playbook` with an inline inventory target.
On macOS it also sets `OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES` for the Ansible process automatically.

If you need to inspect the discovered IP manually:

```bash
bash ./deployments/utm/determine-vm-ip-address.sh --arch $arch --os windows11
```

Related guide:

- [Windows Packer README](../build/packer/windows/README.md)

Newly built Windows images install a dedicated WinRM firewall rule for TCP `5985` on all network profiles, so later NIC or network-profile changes should not break reachability.
Older images may still need their network switched to `Private` or an equivalent firewall rule added manually.

### Ubuntu on macOS with UTM

Install host dependencies first:

```bash
sailwright install
```

Build and create the Ubuntu VM:

```bash
arch=arm64 # or amd64
type=desktop # or server
sailwright build ubuntu --arch $arch --type $type
sailwright create ubuntu --arch $arch --type $type
```

Open UTM and start the created VM.

Set Ubuntu SSH credentials in project-root `.env` or the process environment:

```dotenv
UTM_UBUNTU_ANSIBLE_USER=packer
UTM_UBUNTU_ANSIBLE_PASSWORD=P@ssw0rd!
UTM_UBUNTU_ANSIBLE_BECOME_PASSWORD=P@ssw0rd!
# Optional (defaults shown):
UTM_UBUNTU_ANSIBLE_CONNECTION=ssh
UTM_UBUNTU_ANSIBLE_SSH_COMMON_ARGS=-o StrictHostKeyChecking=no -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o ControlMaster=no -o ControlPersist=no
UTM_UBUNTU_ANSIBLE_SSH_TIMEOUT=120
UTM_UBUNTU_ANSIBLE_SSH_RETRIES=3
```

Provision it from the repository root:

```bash
sailwright provision ubuntu --type $type --arch $arch --check
sailwright provision ubuntu --type $type --arch $arch
```

The wrapper discovers the VM IP automatically from the generated UTM config and `arp -a`, then runs `ansible-playbook` with an inline inventory target.
On macOS it also sets `OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES` for the Ansible process automatically.

If you need to inspect the discovered IP manually:

```bash
bash ./deployments/utm/determine-vm-ip-address.sh --arch $arch --os "ubuntu-$type"
```
