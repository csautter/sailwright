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
  [agent gateway](../docs/agent-gateway.md)) and `~/.claude-devcontainer`

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
