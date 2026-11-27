# Devcontainer

Open the repository in a devcontainer; no setup is needed.

`initialize-host.sh` runs on the host before each start and writes
`.generated/compose.local.yaml` (gitignored) with the bind mounts for your
machine. Optional sources are mounted only if they exist, so a missing path
never blocks startup. The creation log lists what was mounted and skipped.

Mounted automatically when present:

- Agent config: `~/.agents/skills` (for Claude and Codex), `~/.agents/agents`,
  `~/.codex`
  (`/root/.codex-host`, only for its `auth.json`), `~/.gemini`
- Sibling repos next to this checkout: `sailwright-plans`,
  `sailwright-roles-private`
- Always: `~/.sailwright-gateway` (read-only, see
  [agent gateway](../docs/agent-gateway.md)), `~/.claude-devcontainer`
  (`/root/.claude`), `~/.codex-devcontainer` (`/root/.codex`) and
  `~/.config/sailwright/devcontainer/github` (read-only, see
  [GitHub CLI and git credentials](#github-cli-and-git-credentials))

## GitHub CLI and git credentials

The image has `gh`. `github-auth.sh host` (run by `initialize-host.sh`) writes
a GitHub token to `~/.config/sailwright/devcontainer/github/token` on the host.
That directory is mounted read-only at `/run/sailwright-github`. On every start,
`github-auth.sh container` logs `gh` in with the token, makes `gh` the git
credential helper for `github.com` and rewrites `git@github.com:` remotes to
HTTPS, so `gh`, `git fetch` and `git push` work without SSH keys.

Anything that runs in the container (a coding agent, or a malicious
`postinstall` script of a dependency) can read the token and use it within
seconds, for example to push a workflow that leaks Actions secrets. So the
container gets no token unless you create one for it, and that token should
only reach the repositories and rights the agents need.

The token comes from:

1. `~/.config/sailwright/devcontainer/github-token`: a one-line token you
   create for the containers (`chmod 600` it). An empty file shares no token at
   all. On Windows and WSL `/mnt` drives, protect the file with NTFS
   ACLs; `chmod` alone may not restrict access.
2. Only with `SAILWRIGHT_GH_SHARE_HOST_LOGIN=1` and no such file: `gh auth
   token` of your host login, with a warning at every start. Every process in
   the container can then use it with all of its scopes (usually `repo`,
   `workflow` and `read:org`, so every repository you can reach), without
   expiry. Set the variable in the environment `initializeCommand` runs in
   (VS Code's, and Orca's for its workspaces). For Orca, the gitignored
   `scripts/orca-vm/local.env` can set it; under WSL, propagate a Windows
   environment setting through `WSLENV`. The shared token is common to
   the devcontainer and all Orca workspaces, so a start without the variable
   removes it again for all of them.

Without either, no token is shared and the start prints how to create one.
**Behavior change:** earlier versions shared the host login without asking;
a container that got it is logged out at its next start or Orca reattach,
including logins marked by the older Orca hash file. Resume and reattach
refresh the host share and stream the current host authentication helper into
container Bash over stdin. This also migrates containers whose independent
checkout still contains an older helper. All stored GitHub accounts are checked
by token hash, including inactive accounts; failed or ineffective logout keeps
the marker for retry and unrelated accounts remain available.

Orca reattach does not replay `initializeCommand` or `postStartCommand`.
A real resume runs `postStartCommand` again, including local services and agent
login links. Before that hook the launcher saves the existing GitHub marker
hashes, restores them afterwards even if the hook fails, and then runs the
current host-owned auth helper. Thus an old checkout's auth hook cannot discard
the evidence needed for logout retries. Resume does not rerun `initializeCommand`;
the auth host step creates its share directory. Recreate to apply changed mounts.
Ordinary VS Code starts retain their configured lifecycle hooks.

Complete multi-account cleanup requires **gh >= 2.81** inside the container
(`gh auth status --json hosts`). If JSON status is unavailable, cleanup falls
back to the active account: the token is held only in a shell variable and
compared by hash before logout. A marker is removed only after confirmed removal
or an explicit empty-store status. An unrelated active account, failed read,
backend error or ineffective logout retains the marker and warns about the
multi-account minimum. Upgrade gh inside an old container (or recreate it) and
retry to clean up inactive accounts. Sailwright's current image meets this
minimum; no host gh upgrade is needed for container account enumeration.

If untrusted code ran with an earlier shared token, revoke it at GitHub
Settings > Applications (GitHub CLI authorization), then run `gh auth login`
on the host again. Logging out a container does not invalidate a copied token.
When replacing a PAT, revoke the old token at GitHub too.

Recommended token: a
[fine-grained personal access token](https://github.com/settings/personal-access-tokens/new)
with

- **Repository access:** Only select repositories, namely the repositories
  the agents work on. A repository created later must be added to the token
  by hand.
- **Repository permissions:** Contents, Pull requests and Issues: read and
  write; Metadata: read. Nothing else: no Workflows, Administration, Secrets
  or Actions write.
- **Expiration:** a date, not "No expiration".

Protect `main` with a ruleset (require a pull request, block force pushes), so
the token cannot change it directly. Known limitation: without the Workflows
permission, agents cannot push changes to `.github/workflows/`; you push
those yourself. That is intended, because pushing a workflow is the usual way
from a stolen token to the repository's Actions secrets.

For classic tokens (`ghp_`, `gho_`, `ghu_`, including the host login), the start asks
the GitHub API for the token's scopes (`X-OAuth-Scopes`) and warns about
`workflow`, `admin:*`, `delete_repo` and `write:packages`. The token is never
printed.

`$XDG_CONFIG_HOME` is honored; under WSL the Windows profile's `.config` is
used. A changed token is picked up at the next container start (Orca: when a
workspace is created, resumed or reattached). When no token is shared any more, a login
made from an earlier shared token is logged out and the HTTPS rewrite
removed; a login you made inside the container yourself is kept. With the
opt-in, a start where no `gh` can be found on the host at all (for example
VS Code running `initializeCommand` with WSL's `bash.exe`) keeps the token
shared earlier.

## Agent sessions

Claude Code and Codex keep their state in devcontainer-owned host directories,
so sessions, history, settings and logins survive a container rebuild:

- `~/.claude-devcontainer` is `/root/.claude`. `CLAUDE_CONFIG_DIR` points there,
  so `.claude.json` (onboarding, project trust, MCP servers) is kept as well.
- `~/.codex-devcontainer` is `/root/.codex` (`auth.json`, `sessions/`,
  `history.jsonl`, `config.toml`). The host `~/.codex` login is not shared, so
  you sign in to Codex separately inside the container and can use a different
  account there. Its `.tmp/` plugin cache and `tmp/` runtime helpers live in
  the `sailwright-codex-tmp` and `sailwright-codex-runtime-tmp` Docker volumes
  instead: Codex rewrites thousands of files in `.tmp/` on every start, and a
  Windows host mount cannot delete the locked helper dirs in `tmp/`.

Delete these directories on the host to reset the agents' state.

## Clone window colors

`clone-color.sh` runs on every start and colors the VS Code window by the
clone's name suffix (`sailwright-a` blue, `-b` green, `-c` orange, `-d` purple,
...), so parallel clones are easy to tell apart. A folder without a `-suffix`
is left uncolored. The colors are written to the container's remote Machine
settings (`~/.vscode-server/data/Machine/settings.json`), not to the tracked
`.vscode/settings.json`. That `Machine` directory is a per-clone volume
(`<clone>-vscode-machine`, added by `initialize-host.sh`), because the rest of
`~/.vscode-server/data` is shared by all clones and one clone's color would
otherwise show up in every window. Edit the palette at the top of the script
to change them.

## Orca workspaces (optional)

[Orca](https://www.onorca.dev) can run each workspace in its own local Docker
container built from this devcontainer: recipe **Docker (devcontainer image)**
in [`orca.yaml`](../orca.yaml), scripts in [`scripts/orca-vm/`](../scripts/orca-vm/).
The scripts read the Compose service from `docker compose config`, so the image,
the generated mounts (including sibling repos, `~/.sailwright-gateway` and
`mounts.local`), `local/container.env` and the lifecycle commands match the VS
Code devcontainer. Workspace creation and `scripts/orca-vm/docker-build.sh`
run `initializeCommand` before resolving the base image. Orca seeds the
repository from a git bundle of your committed branches. The image installs the
Claude Code and Codex CLIs for Orca workspaces without a VS Code server. Once
Orca installs its CLI in the workspace, SSH login shells include it on `PATH`.

Orca workspaces from the same checkout share per-checkout Docker volumes named
`orca-<checkout-folder>-claude` and `orca-<checkout-folder>-codex`. They store
the Orca Claude and Codex logins, history and settings across workspace
recreation, separate from the host and the VS Code devcontainer so you can use
different accounts. The first start seeds `.claude.json`, `settings.json` and
`config.toml` from the read-only VS Code devcontainer state (never credentials).
Sign in to Claude and Codex once in Orca to create their logins. Existing Orca
containers need to be recreated to pick up the new mounts. The workspace removal
workflow fetches branch commits, so commit any work you want to keep before
recreating.

Use `ORCA_DOCKER_CLAUDE_VOLUME` / `ORCA_DOCKER_CODEX_VOLUME` in the environment
or `scripts/orca-vm/local.env` to choose unique names if two checkouts have the
same folder name. To reset an Orca agent login, settings and history for this
checkout, remove the Orca containers using the volume, then run
`scripts/orca-vm/docker-workspace.sh claude-logout` or
`scripts/orca-vm/docker-workspace.sh codex-logout`.

- Turn on **Settings → Experimental → Cloud VM** in Orca; the recipe only shows
  up under **Run on** after that, and only while `orca.yaml` is on `main`.
- Works on macOS and on Windows (Orca runs `scripts/orca-vm/recipe.cmd`, which
  starts the scripts with Git Bash). Needs Docker, `jq` and Git for Windows
  (`winget install jqlang.jq` for `jq`).
- The image uses no devcontainer features (Python comes from Debian's
  `python3` packages in the `Dockerfile`), so Orca builds it with plain
  Docker and needs no devcontainer CLI.
- Branches made in a workspace are fetched to
  `refs/remotes/orca/<container>/*` when it is deleted
  (`scripts/orca-vm/docker-fetch.sh` fetches them on demand), so unpushed
  work is not lost. With shared git credentials, workspaces can also push to
  `origin` themselves.
- Git credentials: Orca workspaces get the same GitHub token as the
  devcontainer, through `github-auth.sh` in the devcontainer lifecycle (see
  [GitHub CLI and git credentials](#github-cli-and-git-credentials)). The
  template's own `share_credentials` GitHub sharing is turned off in
  `scripts/orca-vm/defaults.env` (`ORCA_DOCKER_GITHUB_AUTH=none`), so there is
  one token file and one opt-in. Until push credentials are available, `[remote
  "origin"]` is withheld in the container's checkout (`orca.originUrl` and
  `refs/remotes/origin/*` stay intact) so Orca's environment doctor treats the
  missing remote as a warning instead of failing workspace creation; `origin` is
  restored automatically once push access works.
  SSH keys are never copied into workspaces: agents run as root there and
  could read or use them, and a personal key opens much more than `origin`.
- `scripts/orca-vm/docker-workspace.sh doctor` checks Docker, Orca and the Cloud
  VM switch; `scripts/orca-vm/tests/run.sh` tests the scripts without Docker.

### KVM passthrough and VM self-tests

The devcontainer image includes architecture-specific QEMU, UEFI firmware,
`swtpm`, libvirt user-session tooling, `virtinst`, `xorriso`, and the runtime
helpers required by unmodified Sailwright builds and provisioning, and sets
`DEV_ALCHEMY_LIBVIRT_URI=qemu:///session` by default (in both `Dockerfile` and
`devcontainer.json`) so Linux and Windows VMs can run without `privileged` mode
or a host libvirt socket:

- **amd64 image:** `qemu-system-x86`, `ovmf`, plus the common packages below.
- **arm64 image:** `qemu-system-arm`, `qemu-efi-aarch64`, plus the common
  packages below.
- **Common packages:** `ipxe-qemu`, `libarchive-tools`, `libvirt-clients`,
  `libvirt-daemon`, `libvirt-daemon-driver-qemu`,
  `libvirt-daemon-driver-storage`, `passt`, `python3-winrm`,
  `qemu-system-modules-spice`, `qemu-utils`, `sshpass`, `swtpm`,
  `swtpm-tools`, `virtinst`, `xorriso`. Desktop GUI viewers
  (`qemu-system-gui`, `virt-manager`, `virt-viewer`) remain omitted.

`initialize-host.sh` adds `devices: ["/dev/kvm:/dev/kvm"]` to
`.generated/compose.local.yaml` only when `/dev/kvm` is available (and logs it
as skipped otherwise). `scripts/orca-vm/docker-lib.sh` translates Compose
`devices` into `docker run --device` arguments and skips any missing device so
devcontainers and Orca workspaces still start cleanly without `/dev/kvm`.
Both scripts check `/dev/kvm` directly for local Linux Docker sockets. For
VM-backed or remote engines they probe the active Docker engine (via
`colima --profile <profile> ssh` when the selected Docker endpoint is a Colima
profile socket, or a Docker probe using an already-local image for other
VM-backed engines) so Apple Silicon M3+/M5 hosts running
`colima start --nested-virtualization` pass `/dev/kvm` through automatically
while hosts without `/dev/kvm` (such as Apple Silicon M2) skip it without
blocking startup.

#### Host variants

| Host | KVM / VM path | Supported guests |
|---|---|---|
| **Ubuntu (AMD, rootless Docker)** | `/dev/kvm` passed into the Orca container (`DEV_ALCHEMY_LIBVIRT_URI=qemu:///session`). Under rootless Docker, container `root` maps to your host user without supplementary groups such as `kvm`, so `/dev/kvm` must grant r/w access to the host user (e.g. via ACL or udev rule). | `amd64`: Ubuntu, Windows 11 |
| **Apple Silicon M3+/M5 (Colima)** | Start Colima with `colima start --nested-virtualization` (requires Apple M3+ and macOS 15+), then recreate the container so `/dev/kvm` is probed in the Colima VM and passed through automatically. | `arm64`: Ubuntu, Windows 11 |
| **Apple Silicon M2 (Colima)** | **No** hardware nested virtualization before M3 (`/dev/kvm` is absent in the Colima VM and container). Use the **gateway path**: run VMs natively on macOS via `sailwright-gateway` (Tart/UTM) while agents stay in the container (see [agent gateway](../docs/agent-gateway.md)). | `arm64`: Ubuntu, Windows 11, macOS |

#### Preflight script

Run `bash scripts/orca-vm/kvm-preflight.sh` inside the container before starting
local KVM self-tests. It checks CPU architecture, `/dev/kvm` presence and r/w
access, `DEV_ALCHEMY_LIBVIRT_URI`, and required VM binaries (`qemu-system-*`,
`qemu-img`, `virsh`, `virt-install`, `passt`, `swtpm`, `xorriso`, `bsdtar`,
`sshpass`):

| Exit code | Meaning |
|---|---|
| `0` | Ready: `/dev/kvm` is readable/writable and all required VM tools are present. |
| `1` | Invalid CLI usage or unsupported CPU architecture. |
| `2` | `/dev/kvm` is missing; prints host-matrix guidance (Ubuntu AMD, M3+/M5 Colima `--nested-virtualization`, and M2 Colima `sailwright-gateway` path). |
| `3` | `/dev/kvm` exists without r/w access; prints rootless Docker ACL/group troubleshooting. |
| `4` | `/dev/kvm` is usable, but one or more required VM tools are missing (rebuild the devcontainer image). |

#### Open verification check (real KVM)

Because the current development container runs inside Colima on an Apple M2 host
without Docker or `/dev/kvm`, the following checks remain open for a real
Docker/KVM host (Ubuntu AMD with rootless Docker or Apple M3+/M5 Colima with
`--nested-virtualization`):

1. Build the updated devcontainer image:
   `docker build -f .devcontainer/Dockerfile .devcontainer`
2. Verify `/dev/kvm` read/write access under rootless Docker:
   `docker run --rm --device /dev/kvm debian:trixie sh -c 'test -r /dev/kvm && test -w /dev/kvm && echo ok'`
3. Recreate an Orca workspace / devcontainer on the KVM host, confirm
   `bash scripts/orca-vm/kvm-preflight.sh` exits `0`, and verify a real
   `qemu:///session` domain launch and Sailwright build/provision flow.

## Extra personal mounts (optional)

Copy `mounts.local.example` to `mounts.local` and add `source:target[:ro]`
lines. Then rebuild the container.

## Local hooks (optional)

For machine-specific setup that must not be committed, such as trusting the
CA of a TLS-inspecting proxy, use these optional files. Each one is used only
if it exists:

| File | Runs | Use |
|---|---|---|
| `~/.config/sailwright/devcontainer/initialize-host.sh` (`$XDG_CONFIG_HOME` is honored) | On the host, before each start, from `initialize-host.sh` | Export files the container needs. `REPO_ROOT`, `REPO_NAME` and `USER_HOME` are set. The workspace is mounted, so files written below `REPO_ROOT` show up at `/workspaces/$REPO_NAME`. A failing hook only prints a warning. |
| `.devcontainer/local/container.env` (gitignored) | Loaded as a Compose `env_file` | `KEY=value` lines for the container environment. Applied when the container is created, so rebuild after a change. The host hook may write it. |
| `.devcontainer/local/post-start.sh` (gitignored, mounted read-only into containers) | In the container, first in `postStartCommand` | Container setup that has to happen before `go mod vendor` and the other start commands reach the network. |

The host hook is deliberately kept outside the workspace. The container mounts
the workspace read-write, so a host hook stored there could be created or
changed from inside the container, for example by a coding agent, and would
then run on the host at the next start without showing up in `git status`.
`initialize-host.sh` ignores `.devcontainer/local/initialize-host.sh` and
prints the command to move it.

## Host prerequisites

- `initializeCommand` runs `bash .devcontainer/initialize-host.sh` on the host.
  On Windows this works with Git Bash or WSL's `bash.exe`, whichever comes
  first on `PATH`. Under WSL, a checkout on a Windows drive uses the Windows
  profile (`%USERPROFILE%`) as home and passes `C:/...` paths to Docker
  Desktop. A checkout inside the WSL filesystem keeps the WSL home.
- On Windows, the workspace is a bind mount from the Windows drive, which is
  noticeably slower for `git` and Go builds than a checkout inside WSL.

## Upgrading from the single-container setup

The devcontainer now uses Docker Compose, which changes the project name. Existing
containers are not reused and keep running or lingering; remove the old one
(`docker rm -f <container>` or `docker compose down`) after rebuilding. Named
volumes are reused.
