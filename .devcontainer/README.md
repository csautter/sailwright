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
refresh the host share before running the container authentication hook.
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

For classic tokens (`ghp_`, `gho_`, including the host login), the start asks
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
- `~/.codex-devcontainer` is `/root/.codex` (`sessions/`, `history.jsonl`,
  `config.toml`). A host `~/.codex/auth.json` (mounted at
  `/root/.codex-host`) is linked in on every start so the login is shared with
  the host. Its `.tmp/` plugin cache and `tmp/` runtime helpers live in the
  `sailwright-codex-tmp` and `sailwright-codex-runtime-tmp` Docker volumes
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
Code devcontainer. Differences: the repository is seeded from a git bundle of
your committed branches instead of being bind-mounted, `.vscode-server` and Codex
state stay per container, and only the agent logins (`link-agent-auth.sh`) and
the git credentials (see below) are shared. Without VS Code there is no
Claude Code extension, so the image installs the Claude Code CLI (pinned in
the `Dockerfile`, self-update off) next to Codex.

- Turn on **Settings → Experimental → Cloud VM** in Orca; the recipe only shows
  up under **Run on** after that, and only while `orca.yaml` is on `main`.
- Works on macOS and on Windows (Orca runs `scripts/orca-vm/recipe.cmd`, which
  starts the scripts with Git Bash). Needs Docker, `jq` and Git for Windows
  (`winget install jqlang.jq` for `jq`).
- The devcontainer feature (Python) needs the devcontainer CLI
  (`npm install -g @devcontainers/cli`). Without it, put
  `ORCA_DOCKER_SKIP_FEATURES=1` in the gitignored `scripts/orca-vm/local.env`;
  the image still has Debian's Python 3.
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
  one token file and one opt-in.
  SSH keys are never copied into workspaces: agents run as root there and
  could read or use them, and a personal key opens much more than `origin`.
- `scripts/orca-vm/docker-workspace.sh doctor` checks Docker, Orca and the Cloud
  VM switch; `scripts/orca-vm/tests/run.sh` tests the scripts without Docker.

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
| `.devcontainer/local/post-start.sh` (gitignored) | In the container, first in `postStartCommand` | Container setup that has to happen before `go mod vendor` and the other start commands reach the network. |

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
