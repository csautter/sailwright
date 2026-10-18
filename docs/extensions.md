# Sailwright Extensions

Sailwright extensions are external executables that integrate through files,
JSON documents, stdin/stdout, and process boundaries. They are not Go plugins
and they are not linked into the `sailwright` binary.

This keeps the Sailwright core small while allowing separate tools, written in
any language and released on their own schedule, to provide extra capabilities
such as system analysis, inventory import, policy checks, or generated Ansible
content.

## Command Surface

Install an extension as an executable on `PATH` named:

```text
sailwright-<name>
```

Examples:

```text
sailwright-analyzer
sailwright-inventory-import
sailwright-policy-check
```

List installed extensions:

```bash
sailwright extension list
```

Run an extension:

```bash
sailwright extension run analyzer -- scan --out snapshot.json
sailwright extension run analyzer -- generate --from snapshot.json --out generated-ansible
```

Arguments after `--` are passed to the extension unchanged. The Sailwright CLI
does not interpret extension-specific flags.

## Execution Contract

When Sailwright runs an extension, it:

- resolves `sailwright-<name>` from `PATH`, using the first directory that
  contains a match
- ignores relative `PATH` entries such as `.`, so a checked-out repository
  cannot inject extensions through the current working directory
- starts it as a separate process without shell parsing
- connects stdin, stdout, and stderr to the current CLI process
- sets `SAILWRIGHT_EXTENSION_PROTOCOL=1`
- sets `SAILWRIGHT_EXTENSION_NAME=<name>`
- exits with the extension's exit code and does not add its own error output
  when the extension fails

Extension names must use letters, digits, `.`, `_`, or `-`, and must not contain
path separators.

### Windows

On Windows, an extension must have one of the file suffixes listed in
`PATHEXT` (by default `.com`, `.exe`, `.bat`, `.cmd`). If one directory holds
several variants of the same extension, the `PATHEXT` order decides.

- Prefer `.exe` extensions. Arguments passed to `.bat` and `.cmd` files are
  re-parsed by `cmd.exe`, which makes quoting of special characters fragile.
- PowerShell scripts (`.ps1`) are not started directly. Wrap them in a small
  `.cmd` or `.exe` launcher that calls `pwsh -NoProfile -File`.

### Security

Every executable on `PATH` that matches `sailwright-<name>` is trusted and runs
with the permissions of the current user, the same model that `git` and
`kubectl` use for their plugins. There is no sandbox. Only install extensions
from sources you trust, especially when provisioning with elevated privileges.

## Recommended Extension Commands

Extensions can expose any command surface they need. For system-analysis
extensions, use these command names unless there is a strong reason to differ:

```bash
sailwright extension run analyzer -- manifest
sailwright extension run analyzer -- scan --out snapshot.json
sailwright extension run analyzer -- generate --from snapshot.json --out generated-ansible
sailwright extension run analyzer -- validate --bundle generated-ansible
```

Recommended meanings:

- `manifest` writes extension metadata matching
  [`sailwright.extension-manifest.v1.schema.json`](../schemas/sailwright.extension-manifest.v1.schema.json)
- `scan` writes a system snapshot matching the draft schema
  [`sailwright.system-snapshot.v1alpha1.schema.json`](../schemas/sailwright.system-snapshot.v1alpha1.schema.json)
- `generate` writes an Ansible bundle and metadata matching the draft schema
  [`sailwright.ansible-bundle.v1alpha1.schema.json`](../schemas/sailwright.ansible-bundle.v1alpha1.schema.json)
- `validate` checks an existing generated bundle before provisioning

## System Analyzer Flow

An analyzer should use Sailwright as the provisioning and test surface, not as
a linked library:

```bash
sailwright extension run analyzer -- scan --out snapshot.json
sailwright extension run analyzer -- generate --from snapshot.json --out generated-ansible
sailwright provision local --playbook generated-ansible/playbooks/site.yml --check
```

Generated bundles should prefer normal Ansible layout:

```text
generated-ansible/
  metadata.json
  inventory/
  playbooks/
  roles/
  group_vars/
  host_vars/
```

The generated `metadata.json` should describe the bundle with the Ansible bundle
schema. Sailwright can then run the generated playbooks with the normal
`sailwright provision` wrapper.

## Versioning

The current process protocol is `SAILWRIGHT_EXTENSION_PROTOCOL=1`.

JSON documents should include one of these schema version values:

```text
sailwright.extension-manifest.v1
sailwright.system-snapshot.v1alpha1
sailwright.ansible-bundle.v1alpha1
```

The `v1alpha1` schemas are drafts. They may still change incompatibly and are
promoted to `v1` once Sailwright itself produces or consumes them. Stable
schemas never change the meaning of existing fields; incompatible changes add a
new schema version instead.
