# Dev Alchemy Extensions

Dev Alchemy extensions are external executables that integrate through files,
JSON documents, stdin/stdout, and process boundaries. They are not Go plugins
and they are not linked into the `alchemy` binary.

This keeps the open Dev Alchemy core useful on its own while allowing separate
open or proprietary tools to provide extra capabilities such as system analysis,
inventory import, policy checks, or generated Ansible content.

## Command Surface

Install an extension as an executable on `PATH` named:

```text
alchemy-<name>
```

Examples:

```text
alchemy-analyzer
alchemy-inventory-import
alchemy-policy-check
```

List installed extensions:

```bash
alchemy extension list
```

Run an extension:

```bash
alchemy extension run analyzer -- scan --out snapshot.json
alchemy extension run analyzer -- generate --from snapshot.json --out generated-ansible
```

Arguments after `--` are passed to the extension unchanged. The open CLI does
not interpret extension-specific flags.

## Execution Contract

When Dev Alchemy runs an extension, it:

- resolves `alchemy-<name>` from `PATH`
- starts it as a separate process without shell parsing
- connects stdin, stdout, and stderr to the current CLI process
- sets `DEV_ALCHEMY_EXTENSION_PROTOCOL=1`
- sets `DEV_ALCHEMY_EXTENSION_NAME=<name>`

Extension names must use letters, digits, `.`, `_`, or `-`, and must not contain
path separators.

## Recommended Extension Commands

Extensions can expose any command surface they need. For system-analysis
extensions, use these command names unless there is a strong reason to differ:

```bash
alchemy extension run analyzer -- manifest
alchemy extension run analyzer -- scan --out snapshot.json
alchemy extension run analyzer -- generate --from snapshot.json --out generated-ansible
alchemy extension run analyzer -- validate --bundle generated-ansible
```

Recommended meanings:

- `manifest` writes extension metadata matching
  [`dev-alchemy.extension-manifest.v1.schema.json`](../schemas/dev-alchemy.extension-manifest.v1.schema.json)
- `scan` writes a system snapshot matching
  [`dev-alchemy.system-snapshot.v1.schema.json`](../schemas/dev-alchemy.system-snapshot.v1.schema.json)
- `generate` writes an Ansible bundle and metadata matching
  [`dev-alchemy.ansible-bundle.v1.schema.json`](../schemas/dev-alchemy.ansible-bundle.v1.schema.json)
- `validate` checks an existing generated bundle before provisioning

## System Analyzer Flow

A closed or open analyzer should use Dev Alchemy as the provisioning and test
surface, not as a linked library:

```bash
alchemy extension run analyzer -- scan --out snapshot.json
alchemy extension run analyzer -- generate --from snapshot.json --out generated-ansible
alchemy provision local --playbook generated-ansible/playbooks/site.yml --check
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
schema. Dev Alchemy can then run the generated playbooks with the normal
`alchemy provision` wrapper.

## Versioning

The current process protocol is `DEV_ALCHEMY_EXTENSION_PROTOCOL=1`.

JSON documents should include one of these schema version values:

```text
dev-alchemy.extension-manifest.v1
dev-alchemy.system-snapshot.v1
dev-alchemy.ansible-bundle.v1
```

Future incompatible changes should add new schema versions instead of changing
the meaning of existing fields.
