# Ansible Role Sources

Sailwright can build the Ansible role search path and default playbook path
from a small YAML config file. This lets you keep the bundled example roles as
a fallback, layer private roles above public roles, or point provisioning at
roles and playbooks you are actively developing.

## Config location

By default, Sailwright reads `ansible-role-sources.yml` from the OS-specific
config directory:

- macOS: `~/Library/Application Support/dev-alchemy/ansible-role-sources.yml`
- Windows: `%APPDATA%\dev-alchemy\ansible-role-sources.yml`
- Linux: `${XDG_CONFIG_HOME:-~/.config}/dev-alchemy/ansible-role-sources.yml`

Set `DEV_ALCHEMY_CONFIG_DIR` to move the config directory, or set
`DEV_ALCHEMY_ROLE_SOURCES_CONFIG` to point at one specific config file. If
`DEV_ALCHEMY_ROLE_SOURCES_CONFIG` names a file that does not exist, Sailwright
fails with an error instead of falling back to the bundled roles.

## Layering model

Sources are applied in the order they appear in the file. Ansible uses the
first matching role name it finds, so put specific override roles before base
role collections.

If the default config file is missing, Sailwright keeps the old behavior and uses the
bundled `./roles` directory. When the config file exists,
`include_default_roles` defaults to `true`, so configured sources are placed
before the bundled roles.

Set `playbook` when this source stack should define the default entrypoint for
bare `sailwright provision local` and VM provisioning commands. Relative
playbook paths are resolved through the configured `playbook_sources` first,
then through the bundled Sailwright project when `include_default_playbooks` is
enabled. The `--playbook` CLI flag still wins when it is set.

If no config playbook is available, Sailwright falls back to
`./playbooks/role-sources-test.yml`, a small smoke/example playbook for
role-source resolution. The broader bundled `./playbooks/setup.yml` is also an
example entrypoint; run it explicitly with `--playbook ./playbooks/setup.yml`
or set it as `playbook` in your config.

```yaml
playbook: custom-setup.yml
include_default_roles: true
include_default_playbooks: true
sources:
  - name: private-overrides
    type: local
    path: /Users/me/src/my-dev-roles

  - name: public-base
    type: git
    url: https://github.com/csautter/sailwright.git
    ref: main
    roles_path: roles
    update: pull

playbook_sources:
  - name: private-playbooks
    type: local
    path: /Users/me/src/my-dev-playbooks
```

## Source types

Local sources use `path`. Relative paths are resolved from the config file
directory.

Git sources use `url`. Sailwright clones them into
`DEV_ALCHEMY_APP_DATA_DIR/cache/ansible-role-sources/` for roles and
`DEV_ALCHEMY_APP_DATA_DIR/cache/ansible-playbook-sources/` for playbooks, then
updates existing checkouts before provisioning. Set `ref` for a branch, tag, or
commit.

Set `roles_path` when a source stores roles in a subdirectory. Set
`playbooks_path` when a playbook source stores playbooks in a subdirectory.
For local or Git projects that contain both roles and playbooks, put both paths
on the same `sources` entry:

```yaml
playbook: setup.yml
include_default_roles: false
include_default_playbooks: false
sources:
  - name: dev-stack
    type: git
    url: https://github.com/example/dev-stack.git
    ref: main
    roles_path: roles
    playbooks_path: playbooks
```

If `include_default_roles` is `false`, make sure `playbook` points at an
entrypoint whose role names exist in your configured sources. When you override
the CLI to run the bundled `./playbooks/setup.yml` example, Sailwright keeps the
bundled `./roles` directory available as a fallback for those setup roles.

Update behavior can be disabled for a Git source:

```yaml
sources:
  - name: pinned-base
    type: git
    url: https://github.com/csautter/sailwright.git
    ref: v1.0.0
    pull: false
```

## Manual Layer Test

The repository includes two tiny role-source folders for hand testing:
`roles_test_1/` and `roles_test_2/`. Put the overlay first to verify that the
shared role resolves from `roles_test_2`.

This is also the built-in CLI fallback while developing role-source resolution:
`role-sources-test.yml` completes quickly and avoids the broader bundled setup
example. Run that setup example explicitly with
`--playbook ./playbooks/setup.yml`.

```yaml
playbook: ./playbooks/role-sources-test.yml
include_default_roles: false
include_default_playbooks: true
sources:
  - name: test-overlay
    type: local
    path: /path/to/sailwright/roles_test_2
  - name: test-base
    type: local
    path: /path/to/sailwright/roles_test_1
```

Then run:

```bash
sailwright provision local --check
```

## Manage sources from the CLI

```bash
sailwright config path
sailwright config sources add ~/src/my-tools --name team
sailwright config sources add https://github.com/example/tools.git --name shared --ref main
sailwright config sources list
sailwright config sources remove shared
```

Adding a source detects `roles/` and `playbooks/`, or a directory containing
roles or playbooks directly. Git sources are cloned into the application cache
for detection. Source names must be unique; adding an existing name fails.
Removing a source keeps its files and cached checkout. Edits preserve existing
YAML comments and key order and replace the file atomically.

Configuration precedence, highest first:

1. The global `--config PATH` flag.
2. `DEV_ALCHEMY_ROLE_SOURCES_CONFIG`.
3. `ansible-role-sources.yml` in the current directory.
4. The OS-specific config directory described above.

Relative source paths are relative to the selected configuration file.
When a project configuration is detected automatically, Sailwright prints its
path to stderr. A starter checkout with this file needs no environment variable:

```bash
sailwright provision local --check
sailwright --config ./team-sources.yml provision catalog --json
```

## Discover tools and setups

```bash
sailwright provision catalog
sailwright provision catalog --os windows --json
sailwright provision catalog --refresh
sailwright provision local --role java --role kubectl --check
sailwright provision local --playbook setup --check
```

The catalog includes configured sources and built-in examples. It detects roles
by `tasks/main.yml` or `tasks/main.yaml` and playbooks by a top-level YAML list
whose plays contain `hosts`. The built-in smoke playbook and test-role folders
are excluded. Role descriptions and platforms come from `meta/main.yml` (or
`.yaml`); playbook descriptions use the first comment. Invalid role metadata
produces an error naming the file.

The first source wins for each name and kind. Shadowed sources are reported.
Compatibility reflects declared platform metadata, not a guarantee that a role
will work. Missing metadata means compatibility is unknown and the entry remains
selectable. Incompatible entries remain visible in the catalog. The wizard
marks them unavailable and excludes them from selection.

Existing Git checkouts are read without fetching. Missing checkouts are cloned
on first use; `--refresh` updates them according to their configured update
policy. Provisioning retains the existing Git update behavior.

`--json` writes one document with `schema_version: 1` and `entries: []`.
Each entry has stable fields `kind` (`role` or `playbook`), `name`, `description`,
`source`, `path`, `platforms` (array of platform names), `compatible` (boolean),
and `shadows` (array of source labels). Empty arrays are emitted as `[]`.
`--os` defaults to the host OS; it affects compatibility only.

Roles execute in the requested order using a private temporary playbook removed
after the run. Choose roles or one playbook per invocation. Explicit playbook
paths continue to work. Running `provision local` without selection flags keeps
the configured default or smoke-playbook fallback.
