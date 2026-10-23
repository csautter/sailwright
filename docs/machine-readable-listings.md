# Machine-readable listings

Every `list` subcommand accepts `--json` and prints a single-line JSON document
for scripts and tools:

```bash
sailwright build list --json
```

```json
{"schema_version":1,"targets":[{"os":"ubuntu","type":"server","arch":"amd64","engine":"qemu","fields":{"build":"missing"},"address":null}]}
```

`schema_version` is `1`. Within a version only fields are added, never removed or
renamed. `fields` carries the command-specific columns of the table output, and
`address` is reserved for the VM network address and is always `null` for now.
