# Managed Application Data

Sailwright stores VM build state, deployment state, caches, and standalone
runtime assets outside the repository in an OS-appropriate app-data directory.
User-editable configuration is stored in an OS-appropriate config directory.

## Default location

These directory and environment variable names intentionally retain the
pre-rename `dev-alchemy` / `DEV_ALCHEMY_*` compatibility contract.

- macOS: `~/Library/Application Support/dev-alchemy`
- Windows: `%LOCALAPPDATA%\dev-alchemy`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/dev-alchemy`

## Managed subdirectories

Under that root, Sailwright manages:

- `cache/` for downloaded files and build artifacts
  - `cache/<os>/qemu-out-<slug>/` holds Packer scratch output of QEMU builds
    (for amd64 a full copy of the disk). It is removed when a build finishes,
    successfully or not, and at the start of the next build for the same target,
    so it is safe to delete after an interrupted build. Older versions wrote it to
    `/tmp/dev-alchemy/qemu-out-*`; those leftovers can be deleted.
- `.vagrant/` for isolated Vagrant state
- `packer_cache/` for Packer plugin and download cache
- `project/` for the embedded runtime project, used whenever the working
  directory is not inside a Sailwright source checkout

## Config location

Sailwright reads user-editable config from:

- macOS: `~/Library/Application Support/dev-alchemy`
- Windows: `%APPDATA%\dev-alchemy`
- Linux: `${XDG_CONFIG_HOME:-~/.config}/dev-alchemy`

The Ansible role-source config is
`ansible-role-sources.yml` in that directory. See
[Ansible Role Sources](./ansible-role-sources.md).

## Overrides and exported paths

You can override the default root by setting
`DEV_ALCHEMY_APP_DATA_DIR`.
You can override the config directory by setting
`DEV_ALCHEMY_CONFIG_DIR`.
You can point Sailwright at a specific project tree (playbooks, inventories,
and scripts), such as a fork or a customized copy, by setting
`SAILWRIGHT_PROJECT_DIR`. The directory must exist. When it is set, Sailwright
skips checkout detection and does not extract the embedded project.

Sailwright also exports these derived paths for helper scripts and manual
workflows:

- `DEV_ALCHEMY_CONFIG_DIR`
- `DEV_ALCHEMY_CACHE_DIR`
- `DEV_ALCHEMY_VAGRANT_DIR`
- `DEV_ALCHEMY_PACKER_CACHE_DIR`

## Standalone runtime assets

On the first standalone run, Sailwright extracts bundled scripts, playbooks,
and other runtime assets into `DEV_ALCHEMY_APP_DATA_DIR/project`.

Later runs keep that managed tree in sync so the standalone `sailwright` binary
can operate without a Sailwright source checkout.
