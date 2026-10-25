# Devcontainer

Open the repository in a devcontainer; no setup is needed.

`initialize-host.sh` runs on the host before each start and writes
`.generated/compose.local.yaml` (gitignored) with the bind mounts for your
machine. Optional sources are mounted only if they exist, so a missing path
never blocks startup. The creation log lists what was mounted and skipped.

Mounted automatically when present:

- Agent config: `~/.agents/{skills,agents}`, `~/.codex/{skills,auth.json}`,
  `~/.local/share/opencode`, `~/.gemini`
- Sibling repos next to this checkout: `sailwright-plans`,
  `sailwright-roles-private`
- Always: `~/.sailwright-gateway` (read-only, see
  [agent gateway](../docs/agent-gateway.md)), `~/.claude-devcontainer`
  (`/root/.claude`) and `~/.codex-devcontainer` (`/root/.codex`)

## Agent sessions

Claude Code and Codex keep their state in devcontainer-owned host directories,
so sessions, history, settings and logins survive a container rebuild:

- `~/.claude-devcontainer` is `/root/.claude`. `CLAUDE_CONFIG_DIR` points there,
  so `.claude.json` (onboarding, project trust, MCP servers) is kept as well.
- `~/.codex-devcontainer` is `/root/.codex` (`sessions/`, `history.jsonl`,
  `config.toml`). `~/.codex/skills` is mounted on top of it, and a host
  `~/.codex/auth.json` is linked in on every start so the login is shared with
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

## Extra personal mounts (optional)

Copy `mounts.local.example` to `mounts.local` and add `source:target[:ro]`
lines. Then rebuild the container.

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
