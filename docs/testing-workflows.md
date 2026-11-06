# Testing Workflows

This guide collects the platform-specific testing workflows that used to live in the main `README.md`.
Use the cross-platform matrix in the root [README](../README.md) for a quick support overview, then use the sections below for concrete commands.

## Local provisioning integration

On Linux or macOS with `ansible-playbook` on PATH, run:

```bash
SAILWRIGHT_LOCAL_INTEGRATION=1 go test ./test/localprovision -count=1 -v -timeout 5m
```

These tests build the CLI once, run it outside the checkout using the embedded
localhost inventory, and use a small fixture that only writes into a temporary
directory. They verify check mode leaves the file absent, a real run creates it,
the second run reports `changed=0`, Ansible stdout reaches the CLI output, and
an Ansible failure produces a nonzero CLI exit with the original exit status.
They skip by default and when Ansible is missing. Set
`SAILWRIGHT_LOCAL_TEST_BIN` to reuse an existing binary.

The **Test Ansible Roles** workflow runs these tests and then the full bundled
`playbooks/setup.yml` through the built CLI on Ubuntu 24.04 LTS (amd64 and arm64)
and macOS 15 (Apple Silicon and Intel). It requires successful check and apply
runs, followed by a second apply with `changed=0` for every host in the recap.
Changed tasks are printed on failure and provisioning logs are uploaded as
artifacts. Spotify's upstream Linux packages support amd64 only; the role
explicitly reports that limitation on arm64.

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

Use `sailwright build <osname> --headless` or `sailwright build all --headless`
to build without a QEMU display window or an automatically opened VNC viewer.
On macOS, this suppresses both the QEMU window and Screen Sharing. VNC recording
remains enabled on macOS and Linux hosts; Windows hosts do not currently record
VNC. The flag applies to `build`, including parallel builds, and does not change
the display behavior of `create` or `start`.

`create`, `start`, and `provision` now refuse bridged or unknown networks with
non-private guest credentials unless you explicitly accept the risk. The UTM
templates still use bridged networking, so their new-VM creation is affected.
Before following those workflows, read
[Guest network security](./guest-security.md) for migration guidance, credential
classification, and per-command or persistent overrides. Provisioning variables
do not change credentials baked into an image.

Use the `list` subcommands to see what your current host supports:

```bash
sailwright build list
sailwright create list
sailwright start list
sailwright provision list
sailwright stop list
sailwright destroy list
```

The `Build` column of `build list` shows `exists` for artifacts completed by a
successful build or pull, `incomplete` while a build runs or after an interrupted
one, `missing`, and `unverified` for artifacts without a completion marker (for
example built by an older sailwright version). Builds skip `exists` and
`unverified` artifacts; rebuild an `unverified` one with `--no-cache` if it is
broken. `create list` only tells whether the artifacts are usable: it shows
`exists` for `exists` and `unverified`, and `missing` otherwise.

Ctrl+C stops all running builds and downloads. A single build on Linux or macOS
owns the terminal, so Ctrl+C reaches Packer directly and Packer stops on its own.
For parallel builds started from a terminal, and on Windows, Sailwright gives
Packer up to 30 seconds to remove its VM. A second Ctrl+C cuts that wait to 3
seconds and skips the VNC video post-processing, and a third one exits
immediately. SIGTERM (for example a gateway `job_cancel`) and Ctrl+C without a
terminal (for example a cancelled CI job) only allow 3 seconds. A build that
already finished successfully when Ctrl+C arrives keeps its artifacts.

Parallel builds (`--parallel`) run without the terminal, so a build script that
asks for a password (for example `sudo`) fails instead of prompting. Run
`sudo -v` first, or build the target on its own.

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

## Named VM instances

Use `--name` to keep a disposable test VM beside a working VM of the same type:

```bash
sailwright create ubuntu --type server --arch amd64 --name agent-test
sailwright start ubuntu --type server --arch amd64 --name agent-test
sailwright vm info ubuntu --type server --arch amd64 --name agent-test
sailwright start list
sailwright start list --json
sailwright destroy ubuntu --type server --arch amd64 --name agent-test
```

The same selector works with `stop` and `provision`. Names use 1–24 lowercase
letters, digits, and internal hyphens; `default` and `all` are reserved. Omitting
`--name` finds your existing VM without migration. Instances share the built
image, with their own writable disks, provider state, and guest hostnames.
Named startup waits for guest hostname setup: QEMU/UTM require the guest agent;
Tart uses the existing Ansible/SSH connection; Hyper-V uses Vagrant's hostname
setup. A Windows guest may reboot after a name change.

`stop all` and `destroy all` affect only defaults. Add `--all-instances` explicitly
to include named VMs. `create` warns about low available disk/RAM without blocking.
Build and pull still operate on the shared image. See the
[naming ADR](./adr/0007-named-vm-instances.md) and
[JSON listing contract](./machine-readable-listings.md).

## Deterministic E2E Scenarios

Automatic PR CI runs the CLI smoke scenario and E2E runner tests. Full
host-modifying scenarios remain opt-in on suitable hosts or disposable runners.
The resulting coverage gap is an explicitly
[accepted risk (ADR 0007)](adr/0007-manual-host-e2e-accepted-risk.md).

The repeated user cycles are covered by deterministic scenarios in
[`test/e2e/scenarios`](../test/e2e/scenarios). Each scenario is a YAML file
that runs as a subtest of `go test -tags e2e ./test/e2e`:

| Scenario | Needs | Covers |
| --- | --- | --- |
| `smoke` | nothing | help output and `list --json` for commands that need no hypervisor |
| `vm-lifecycle` | `SAILWRIGHT_E2E_TARGET` | target VM must not exist yet (preflight), pull or build, create, start, provision `--check`, provision, idempotent re-run, stop, destroy (always) |
| `destroy-idempotent` | `SAILWRIGHT_E2E_TARGET` | destroying twice succeeds |
| `local-check` | host changes allowed | `provision local --check` with the default playbook |
| `vm-lifecycle-linux` | Linux host | the `vm-lifecycle` steps for every VM option of a Linux host |
| `vm-lifecycle-macos` | macOS host | the same for every VM option of a macOS host (Tart, UTM) |
| `vm-lifecycle-windows` | Windows host | the same for every VM option of a Windows host (Hyper-V) |
| `cli-readonly` | nothing | `version`, gateway metadata, listings with network and credential metadata, stable local provisioning, role catalog, config path |
| `vm-access-cli` | `SAILWRIGHT_E2E_TARGET` | `vm identity` absent/stopped/running, `vm info` with a masked password, no implicit start, a second create/destroy cycle |
| `guest-security-guard` | a target with bridged or unknown planned network | `create`, `start` and `provision` refuse non-private credentials and work with `--allow-insecure-credentials`; `stop` and `destroy` are not blocked |
| `build-cancel` | `SAILWRIGHT_E2E_TARGET` | a cancelled build is not reported as complete |

The opt-in Go tests `TestVMAccess`, `TestWindowsVMAccess` and `TestVMOwnership`
(gateway ownership, approval after recreation, lock contention; needs a
`--bypass-approvals` gateway and `SAILWRIGHT_VM_OWNERSHIP_E2E=1`) cover guest
access through a real gateway. The [release test kit](../test/release/README.md)
runs these together with host checks that scenarios cannot express.

### E2E CI coverage decision

Routine PR CI runs the tagged E2E `smoke` scenario. Full VM lifecycle scenarios
remain opt-in for targeted validation on suitable hosts. This is an accepted
project decision: running those scenarios routinely would substantially increase
CI load while duplicating much of the existing unit, VM build, deploy smoke,
and provisioning test coverage.

The absence of these lifecycle scenarios from routine CI is therefore an
intentional coverage tradeoff. Code reviews should assess whether a change
introduces a specific behavior that existing automated tests do not cover and
recommend a focused test for that behavior. They should not repeatedly report
the opt-in lifecycle suite as a missing-CI defect or recommend enabling it on
every PR. Reconsidering that CI cost requires an explicit decision to revisit
the tradeoff.

### Running E2E scenarios

The per-host scenarios run once per target, each as its own subtest, e.g.
`TestScenarios/vm-lifecycle-linux/ubuntu-server-amd64`. On other hosts they are
skipped. Targets that the CLI marks unstable on this host (emulated
architectures) are skipped unless `SAILWRIGHT_E2E_INCLUDE_UNSTABLE=1`.
`SAILWRIGHT_E2E_TARGET` narrows a matrix to one target. The run fails when
`create list` offers a target that the scenario does not test, so new VM
options get added to the matrix.

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

# every VM option of this host (one subtest per target)
SAILWRIGHT_E2E_EXECUTOR=gateway go test -tags e2e ./test/e2e \
  -run 'TestScenarios/vm-lifecycle-(linux|macos|windows)' -count=1 -timeout 24h -v

# gateway, from the devcontainer
SAILWRIGHT_E2E_EXECUTOR=gateway SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64 \
  go test -tags e2e ./test/e2e -run 'TestScenarios/(smoke|vm-lifecycle)' -count=1 -timeout 6h -v
```

| Variable | Meaning |
| --- | --- |
| `SAILWRIGHT_E2E_EXECUTOR` | `direct` (default) or `gateway` |
| `SAILWRIGHT_E2E_BIN` | Sailwright binary for the direct executor |
| `SAILWRIGHT_E2E_TARGET` | `os/type/arch[/engine]`, e.g. `ubuntu/server/amd64` or `windows11//amd64/hyperv`; selects the target of `vm-lifecycle` and narrows the per-host matrices |
| `SAILWRIGHT_E2E_INCLUDE_UNSTABLE` | `1` also runs matrix targets the CLI marks unstable |
| `SAILWRIGHT_E2E_PULL_REF` | pull this OCI artifact instead of building in `vm-lifecycle` |
| `SAILWRIGHT_E2E_ALLOW_HOST_CHANGES` | `1` lets the direct executor run host-changing steps |
| `SAILWRIGHT_E2E_OUT` | report directory (default `test/e2e/out`) |
| `SAILWRIGHT_GATEWAY_URL`, `SAILWRIGHT_GATEWAY_TOKEN`, `SAILWRIGHT_GATEWAY_DIR` | optional overrides; by default the gateway executor finds the gateway like `sailwright-gateway connect` (see [Agent Gateway](./agent-gateway.md#quick-start)) |

With `-v`, command output is shown live, prefixed with `|`. Long downloads
log a progress line every 10 seconds when no terminal is attached.

Each run writes `<scenario>.json` and `<scenario>.md` reports with the host,
binary hash and per-step results. For JUnit output, run the same command with
`gotestsum --junitfile`.

### Writing scenarios

```yaml
name: my-scenario            # must match the file name
description: What this checks.
requires: [target]           # target and/or host-changes
# For a matrix instead of SAILWRIGHT_E2E_TARGET:
# host_os: linux             # linux, darwin or windows
# covers: create             # fail if `create list` offers a target missing below
# targets: [ubuntu/server/amd64, windows11//amd64/hyperv]
preflight:                   # checks before anything changes on the host
  - name: target VM does not exist yet
    op: list
    with: {command: destroy}
    expect:
      state: {command: destroy, field: state, regex: "^missing$"}
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
  - name: cli call for the scenario target
    op: cli                  # runs `vm identity <os> --arch <arch> --type <type>`; args_after follow the target
    with: {args: ["vm", "identity"], target: true}
  - name: accept the guest credential risk
    op: create               # create, start and provision only; needs approval through the gateway
    with: {allow_insecure_credentials: true}
  - name: only where the target can be built
    op: build
    when_listed: build       # skip if the target is not in `build list`
  - name: cancel a running build
    op: build
    cancel_after: 3m         # cancel like job_cancel; passes if still running then
    expect:                  # only state is checked after a cancel
      state: {command: build, field: build, regex: "^(missing|incomplete)$"}
always:
  - name: cleanup
    op: destroy
```

After a failed step the remaining steps are skipped; `always` steps still run.
If a `preflight` step fails, nothing else runs, not even `always`: the scenario
has not changed the host, so its cleanup must not touch what was already there.
The lifecycle scenarios use this to stop before building when the target VM
already exists, instead of destroying it at the end. Remove a leftover VM with
`sailwright destroy <os> [--type ...] --arch ...` and run the scenario again.
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

OCI transfers use HTTP/1.1, including redirects to blob storage, to avoid
HTTP/2 `PROTOCOL_ERROR` failures partway through large VM image downloads.
This transport setting is limited to the OCI client; no `GODEBUG` override is needed.
Interrupted blob reads retry up to five times with exponential backoff. When the
registry supports byte ranges, downloads resume from the last received byte;
otherwise the client reads the prefix again. Only newly received bytes count
toward progress, and artifacts must still pass size and digest verification
before entering the local cache. Resumption applies within the running pull;
restarting the CLI starts a new download.

The Linux GitHub Actions build workflow publishes completed Ubuntu artifacts to
GitHub Container Registry on pushes to `main` and on manual `workflow_dispatch`
runs started from `main`. Artifacts are stored under
`ghcr.io/<owner>/ubuntu-24` with tags using the
`<type>-<arch>-<hostos>-build` schema, for example:

```bash
ghcr.io/csautter/ubuntu-24:server-amd64-linux-build
ghcr.io/csautter/ubuntu-24:desktop-arm64-linux-build
```

These are Sailwright VM disk artifacts, not Docker images, so `docker pull`
(which the GHCR package page suggests) does not work. Use `sailwright pull`:

```bash
sailwright pull ghcr.io/csautter/ubuntu-24:server-arm64-linux-build \
  --os ubuntu \
  --type server \
  --arch arm64
```

The artifacts are built on Linux with QEMU and work on Linux and macOS hosts.
On macOS, `sailwright pull` asks before using a Linux-built artifact for UTM;
pass `--yes` to accept it without prompting. Pick the arch that matches the
host CPU. Windows hosts use Hyper-V and cannot use these artifacts, and Windows
VM build artifacts are intentionally not published.

The `Test Pull of Published OCI Images` workflow
(`.github/workflows/test-pull-published-images.yml`) runs weekly and on changes
to the OCI code. It checks that all four published tags resolve to a VM build
artifact with matching annotations, and pulls `server-amd64` anonymously and
`server-arm64` with `GITHUB_TOKEN` through the CLI, then verifies the pulled
qcow2 image.

Because local VM builds take a long time, an interactive
`sailwright build ubuntu ...` on Linux and macOS hosts offers to pull the
matching published artifact instead. The default answer is no, which builds
locally; a failed pull also falls back to the local build, while Ctrl+C during
the pull stops the command. The pull is anonymous and ignores Docker
credentials, because the images are public. The offer is skipped when the
artifact already exists, with `--no-cache` or `--no-prebuilt`, when
`SAILWRIGHT_NO_PREBUILT_OFFER` is set to a non-empty value, for `build all`,
and when stdin is not a terminal (including `/dev/null` and agent gateway
jobs). `scripts/quickstart.sh` sets `SAILWRIGHT_NO_PREBUILT_OFFER=1` when it
records a session without a terminal on stdin, because the recorder would
otherwise give the build a terminal nobody can answer.

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

The `Test Ask Become Pass` workflow (`.github/workflows/test-ask-become-pass.yml`)
covers `sailwright provision local -K` end to end on GitHub-hosted Linux and
macOS runners. It creates a user whose sudo needs a password and runs
`test/askbecomepass` as that user in a pseudo-terminal: Ansible's become prompt
must reach the terminal despite Sailwright's line-prefixed output, it must be
asked exactly once, and the become tasks must then run as root. The tests change
the host, so they only run when `SAILWRIGHT_ASKPASS_BIN` is set; see the module
docstring for running them on a disposable machine.

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

WinRM credentials default to the `Administrator` / `P@ssw0rd!` account baked into the Windows images. Override them in project-root `.env` or the process environment if you changed it:

```dotenv
# Optional (defaults shown):
LIBVIRT_WINDOWS_ANSIBLE_USER=Administrator
LIBVIRT_WINDOWS_ANSIBLE_PASSWORD=P@ssw0rd!
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

CI build workflows never download the ISO from Microsoft; they read a versioned copy from the build
cache. See [Windows 11 ISO cache](./windows-iso-cache.md).

Build details live here:

- [Windows Packer README](../build/packer/windows/README.md)
- [Windows Hyper-V deployment README](../deployments/vagrant/ansible-windows/README.md)

After the VM is running, provision it from the repository root:

```powershell
sailwright.exe provision windows11 --arch amd64 --check
sailwright.exe provision windows11 --arch amd64
```

WinRM credentials default to the `Administrator` / `P@ssw0rd!` account baked into the Windows images. Override them in project-root `.env` or the process environment if you changed it:

```dotenv
# Optional (defaults shown):
HYPERV_WINDOWS_ANSIBLE_USER=Administrator
HYPERV_WINDOWS_ANSIBLE_PASSWORD=P@ssw0rd!
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

For CI tiers, local profile checks and coverage limits, see
[macOS validation](./macos-ci.md).

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

WinRM credentials default to the `Administrator` / `P@ssw0rd!` account baked into the Windows images. Override them in project-root `.env` or the process environment if you changed it:

```dotenv
# Optional (defaults shown):
UTM_WINDOWS_ANSIBLE_USER=Administrator
UTM_WINDOWS_ANSIBLE_PASSWORD=P@ssw0rd!
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


## Named instance host checks

After building the Ubuntu image and starting a gateway pinned to the matching
CLI, run the opt-in two-guest test:

```bash
SAILWRIGHT_NAMED_VM_E2E=1 SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64 \
  go test -tags e2e ./test/e2e -run '^TestNamedVMInstances$' -count=1 -timeout 1h -v
```

This refuses pre-existing `e2e-a`/`e2e-b`, creates and starts both from one image,
checks distinct addresses and hostnames through SSH tunnels, checks both list
rows, destroys `e2e-a`, and checks SSH to `e2e-b` and unchanged default state.
Cleanup destroys both test names even after failures. Allow enough RAM/disk and
room under `max_instances` for two guests. It requires `ssh`, `sshpass`, and
`sailwright-gateway` on the controller PATH.

Portable tests cover all four providers' names/paths and legacy defaults. Before
releasing on real macOS/Windows hosts, verify two named instances and a legacy
default together, including create/start/info/provision/stop/destroy:

- UTM: bundle registration, fresh UUID/MAC, guest-agent hostname setup and bridged
  DHCP leases. Verify both architectures supported by the host.
- Tart: independent clones/MACs, configured SSH hostname setup, bridged mode where
  used, and rejection of `TART_MACOS_VM_NAME` together with `--name`.
- Hyper-V: distinct Vagrant state and VM disks, switch/address discovery through
  `Get-VMNetworkAdapter`, hostname/reboot behavior, and management from a second
  app-data directory with no Vagrant state. Verify another VM's shared disk is
  retained by the direct destroy fallback.
- Linux libvirt: simultaneous SSH, independent DHCP identities (especially with
  older images), dynamically allocated SPICE endpoints, and clone/build locking.

Creation metadata lives under app-data `instances/<engine>/` with schema version
1, logical identity, provider name/ID, creation time, and image version. An unknown
image version is an empty string. It is supplemental: deleting this metadata must
not hide a VM from discovery, and stale metadata never proves a VM still exists.
