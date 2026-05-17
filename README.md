# Sailwright

**Sailwright** is an opinionated cross-platform automation toolkit for setting
up, testing, and maintaining developer environments on **macOS**, **Linux**,
and **Windows**. It combines [Ansible](https://www.ansible.com/) with
host-specific VM and system tooling so teams can manage local machines, remote
hosts, and disposable test systems through one repository.

It is especially useful when a team has a mixed device fleet and wants one
repeatable way to:

- onboard developers faster
- keep machine setup consistent across operating systems
- test provisioning changes safely before rolling them out
- reduce one-off scripts, tribal knowledge, and repeated support work

Sailwright is not a replacement for classic MDM/UEM tooling. It complements
those tools by handling the developer-tooling and workflow layer that often
remains manual, team-specific, and hard to reproduce.

Unlike raw `ansible-playbook` runs, chezmoi, Nix/Devbox, or devcontainers,
Sailwright pairs Ansible with disposable, host-specific VM build/provision
tooling so the same playbooks can be tested against real macOS/Linux/Windows
targets before they ever touch a developer's machine.

> _"Transform your system into a dev powerhouse with a touch of automation magic."_

<p align="center">
  <img src="./demo/quickstart.gif" width="49%" alt="Sailwright quick-start: install, build, create, start, and provision a VM" />
  <img src="./demo/vm-build.gif" width="49%" alt="The VM autoinstall running unattended during the build" />
</p>

<p align="center"><sub>Left: the real quick-start commands. Right: a real, unattended VM build
(Ubuntu autoinstall) — no clicks, fully automated. See <a href="./demo/">demo/</a> and
<code>make demo-build-video</code>.</sub></p>

## Quick Start

```bash
TAG="$(curl -fsSL https://api.github.com/repos/csautter/sailwright/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${TAG#v}_linux_amd64.tar.gz" && tar -xzf sailwright_*_linux_amd64.tar.gz
./sailwright install && ./sailwright build list
```

See [🚀 Getting Started](#-getting-started) below for macOS/Windows steps,
cloning the repo instead, and running your first full workflow.

## Why It Helps

Without a shared automation baseline, cross-platform teams usually run into the
same problems:

- developers spend hours figuring out how a machine is supposed to look
- onboarding depends too much on senior team members
- setup differences become hard to debug and reproduce
- OS-specific scripts drift over time

Sailwright addresses that by keeping setup logic in versioned Ansible roles
and playbooks, then using host-appropriate tooling to apply and test them.

## Support Snapshot

The project currently supports these host-to-target workflows:

| Host OS | What you can automate today |
| --- | --- |
| **macOS** | Managed workflows for **macOS** (Tart), **Ubuntu** (UTM), and **Windows 11** (UTM) |
| **Windows** | Managed workflows for **Ubuntu** (Hyper-V) and **Windows 11** (Hyper-V, plus VirtualBox as unstable) |
| **Linux** | Managed workflows for **Ubuntu** and **Windows 11** with native **QEMU/KVM** via **libvirt/virt-manager**, plus direct Ansible runs and Docker-based Linux testing |

This means Sailwright can cover every currently supported guest OS family on a
**macOS** host, and every currently supported family except
**macOS** on a **Windows host** or **Linux** host.

Use the built-in discovery commands to see the exact combinations available on
your current machine:

```bash
sailwright build list
sailwright create list
sailwright start list
sailwright provision list
sailwright stop list
sailwright destroy list
```

> Note: macOS guests are only supported on macOS hosts due to Apple platform
> and licensing restrictions.

## Base Model

Sailwright follows a few simple ideas:

- **Ansible roles and playbooks are the source of truth** for machine setup
- **roles stay cross-platform where possible**, with OS-specific handling where needed
- **runs are idempotent**, so the same workflow can be applied repeatedly
- **execution is pull-oriented**, so machines can run their own automation locally
- **`--check` mode matters**, so changes can be previewed before they apply

That makes it practical both for daily developer use and for testing changes in
VMs before applying them to real machines.

You can also use the built-in wrapper for host-local provisioning:

```bash
sailwright provision local --check
sailwright provision local --proto ssh --check
sailwright provision local --playbook ./playbooks/setup.yml --check
sailwright provision local
```

Use this when you want the same command surface on your real workstation that
you use for managed test targets. A bare run uses the playbook from configfile
`ansible-role-sources.yml` when configured; otherwise it falls back to the
bundled role-source smoke/example playbook. For platform defaults, Windows
transport behavior, cleanup flags, and rollback steps, see
[Local Provisioning](./docs/local-provisioning.md) and
[Windows Ansible Access](./docs/windows-ansible-access.md). For direct
`ansible-playbook` examples, use
[Running Playbooks](./docs/running-playbooks.md).

## 🚀 Getting Started

### 1. Choose how you want to run it

You can either:

- download a release binary for normal use
- clone the repository when you want to edit playbooks, roles, or project code

#### Download a release binary

Release assets are published on the
[GitHub Releases page](https://github.com/csautter/sailwright/releases) as
`sailwright_<version>_<os>_<arch>`.

After extraction, the executable is named `sailwright` on macOS/Linux and
`sailwright.exe` on Windows.

If you prefer the shorter `sail` command, create it yourself as a shell alias
such as `alias sail=sailwright`, or install a `sail` symlink that points to the
`sailwright` executable.

macOS / Linux example:

```bash
TAG="$(curl -fsSL https://api.github.com/repos/csautter/sailwright/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
VERSION="${TAG#v}"
curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_linux_amd64.tar.gz"
tar -xzf "sailwright_${VERSION}_linux_amd64.tar.gz"
chmod +x ./sailwright
./sailwright build list
```

Windows example:

```powershell
$Release = Invoke-RestMethod "https://api.github.com/repos/csautter/sailwright/releases/latest"
$Tag = $Release.tag_name
$Version = $Tag.TrimStart("v")
Invoke-WebRequest -OutFile "sailwright_${Version}_windows_amd64.zip" "https://github.com/csautter/sailwright/releases/download/$Tag/sailwright_${Version}_windows_amd64.zip"
Expand-Archive "sailwright_${Version}_windows_amd64.zip" -DestinationPath .
.\sailwright.exe build list
```

When you run a release binary outside a Git checkout, Sailwright extracts its
embedded runtime assets into a managed app-data directory. See
[Managed Application Data](./docs/managed-application-data.md) for the default
locations and override options.

#### Clone the repository

```bash
git clone https://github.com/csautter/sailwright.git
cd sailwright
```

### 2. Install host dependencies

#### macOS

```bash
sailwright install
```

This runs
[scripts/macos/sailwright-install-dependencies.sh](./scripts/macos/sailwright-install-dependencies.sh).

#### Ubuntu / Debian

```bash
sailwright install
```

This runs
[scripts/linux/sailwright-install-dependencies.sh](./scripts/linux/sailwright-install-dependencies.sh).

On macOS, Linux, and Windows, `sailwright install --with-go` also bootstraps the
Go toolchain.

#### Windows

Run the command from your normal shell. Windows will prompt for elevation
through UAC if needed:

```powershell
sailwright.exe install
# optionally with Go and VirtualBox support:
sailwright.exe install --with-go --virtualbox
```

This runs
[scripts/windows/sailwright-self-setup.ps1](./scripts/windows/sailwright-self-setup.ps1).

### 3. Discover what your host supports

Start with the `list` commands before running a longer workflow:

```bash
sailwright build list
sailwright create list
sailwright provision list
```

Use `--help` when you want the supported flags for a command:

```bash
sailwright --help
sailwright build --help
sailwright provision --help
```

### 4. Run your first useful workflow

There are two common entry paths.

#### A. Configure the current machine directly

Use the built-in wrapper first when you want the shared command surface:

```bash
sailwright provision local --check
sailwright provision local --proto ssh --check
sailwright provision local --playbook ./playbooks/setup.yml --check
sailwright provision local
```

Configure `ansible-role-sources.yml` when you want the bare command to select
your machine setup automatically. Without that config, it runs the bundled
role-source smoke/example playbook. For more wrapper examples and
platform-specific local behavior, use [Local Provisioning](./docs/local-provisioning.md).
For direct localhost or remote-target `ansible-playbook` examples, use
[Running Playbooks](./docs/running-playbooks.md).

#### B. Test the setup in a disposable VM first

Example on a supported host:

```bash
sailwright build ubuntu --type server --arch amd64
sailwright create ubuntu --type server --arch amd64
sailwright provision ubuntu --type server --arch amd64 --check
sailwright provision ubuntu --type server --arch amd64
```

If you are targeting Windows and remote access is not configured yet, start
with [Windows Ansible Access](./docs/windows-ansible-access.md).

When you want to share reusable build artifacts through an OCI registry instead
of rebuilding them locally, use `sailwright push` and `sailwright pull`; see the
[OCI build artifact registry workflow](./docs/testing-workflows.md#oci-build-artifact-registry-workflow).

## Trust & Privilege Model

Sailwright collects no telemetry and phones home to nothing: it makes no
network calls except the ones you explicitly ask for (release/self-update
downloads, OCI registry push/pull, and the Ansible/VM/network operations your
playbooks define).

Elevation is scoped and temporary rather than "run everything as root/admin."
On Windows, `sailwright provision local` creates a dedicated local admin
account with a random or rotated password, enables WinRM-over-HTTPS or SSH
only on the loopback address for the duration of the run, and restores the
prior WinRM/SSH/firewall state during cleanup — see
[Windows Ansible Access](./docs/windows-ansible-access.md#security-note) for
the exact mechanism. On macOS/Linux, install and provisioning scripts prompt
for `sudo` only for the specific steps that need it.

## Docs Map

The root README is the fast entry point. Use these guides when you want the
next level of detail:

- [Local Provisioning](./docs/local-provisioning.md) for the
  `sailwright provision local` wrapper, platform defaults, and Windows cleanup
  flags
- [Running Playbooks](./docs/running-playbooks.md) for direct localhost,
  remote-host, VM, and Windows `ansible-playbook` examples
- [Testing Workflows](./docs/testing-workflows.md#oci-build-artifact-registry-workflow)
  for OCI artifact registry push/pull and host-specific VM and Docker test
  flows
- [Automated Quick-Start](./docs/quickstart-automation.md) for the recorded,
  single-command end-to-end run of the full VM lifecycle
- [Demo Recordings](./docs/demo-recordings.md) for how the quick-start
  recordings and website terminal demo are produced
- [Managed Application Data](./docs/managed-application-data.md) for cache,
  runtime, and app-data locations
- [Ansible Role Sources](./docs/ansible-role-sources.md) for layering local
  and Git-backed role roots during provisioning
- [Extensions](./docs/extensions.md) for the external executable interface and
  JSON schemas used by analyzers and generators
- [Windows Ansible Access](./docs/windows-ansible-access.md) for manual WinRM
  and SSH setup on Windows targets plus OpenSSH rollback notes
- [Example Ansible Roles](./docs/example-roles.md) for the current sample role
  catalog and repository layout
- [Troubleshooting Guide](./docs/troubleshooting.md) for rare host-specific
  issues
- [Licensing FAQ](./docs/licensing.md) for free and commercial use under the
  AGPLv3 and the optional commercial license

## 📦 Example Roles

The repository already includes example roles for common developer tooling such
as `brew`, `java`, `jetbrains`, `k9s`, `kind`, `kubectl`, `kubelogin`,
`openssh`, `python`, and `spotify`.

Use [Example Ansible Roles](./docs/example-roles.md) as the catalog and
[`roles/`](./roles/) as the source of truth.

## 🤝 Contributing

Contributions are welcome. Good areas to improve include:

- more roles and playbooks
- broader cross-platform coverage
- better docs and troubleshooting guidance
- bug fixes and test coverage

See [CONTRIBUTING.md](./CONTRIBUTING.md) for contribution terms and the
Contributor License Agreement (CLA).

## 📜 License

Copyright (c) 2026 Carl-Christian Sautter.

Sailwright is free and open source under the **AGPLv3**: use it at no cost
across your whole organization, including for commercial purposes. A separate
[commercial license](./LICENSE_COMMERCIAL.md) is available only if you want to
embed Sailwright in a closed-source product or service.

See [LICENSE.md](./LICENSE.md) and the [Licensing FAQ](./docs/licensing.md)
for what the AGPLv3 requires.

## 💡 Inspiration

This project grew out of real onboarding and support pain in mixed-OS teams.
The goal is simple: make machine setup reproducible, testable, and much less
dependent on memory or hand-written host-specific scripts.
