# Machine-readable listings

Every `list` subcommand accepts `--json` and prints a single-line JSON document
for scripts and tools:

```bash
sailwright build list --json
```

```json
{"schema_version":2,"targets":[{"name":"","os":"ubuntu","type":"server","arch":"amd64","engine":"qemu","fields":{"build":"missing"},"address":null,"network_mode":"unknown","network_detail":"libvirt domain inspection failed","credentials_origin":"legacy"}]}
```

Listing `schema_version` is `2`. Version 2 adds `name`: an empty string is the
existing default instance, otherwise it is the instance selector. VM lifecycle
listings include discovered named instances as separate rows; artifact listings
still describe shared images. The text table shows unnamed instances as `default`.
The gateway accepts both listing versions 1 and 2. Within a version only fields are added, never removed or
renamed. `fields` carries the command-specific columns of the table output, and
`address` is reserved for the VM network address and is always `null` for now.

`create list --json` also reports `"exists": true|false` per target, which says
whether the VM exists. Tools should use it rather than the display text in
`fields.create` (such as `already created`), which may change. Other listings
omit `exists`.

The `build` field of `build list` is one of:

| Value | Meaning | Used by builds and VM commands |
|---|---|---|
| `exists` | Completed by a successful build or pull | yes |
| `unverified` | Present without a completion marker, e.g. built by an older version | yes |
| `incomplete` | A build or pull is running or was interrupted, or the artifact no longer matches its completion marker | no, rebuilt |
| `missing` | No artifact | no |

Treat values you do not know as not usable; new values may be added within
schema version `1`.

## VM connection details and identity

`sailwright vm info <os> [--type T] [--arch A] [--name N]` shows how to reach a running
VM. Text output masks the guest password; add `--show-password` to print it.
`--json` prints a versioned document that always includes the credentials:

```json
{"schema_version":1,"name":"","os":"ubuntu","type":"server","arch":"amd64","engine":"qemu","state":"running","address":"192.168.122.40","network_mode":"nat","protocol":"ssh","port":22,"username":"packer","password":"…","instance_id":"8f0c2a1e-3b4d-4c5e-9f60-718293a4b5c6"}
```

`sailwright vm identity <os> [--type T] [--arch A] [--name N]` prints whether the VM
exists and its hypervisor instance id, also for a stopped VM:

```json
{"schema_version":1,"name":"","os":"ubuntu","type":"server","arch":"amd64","engine":"qemu","exists":true,"instance_id":"8f0c2a1e-3b4d-4c5e-9f60-718293a4b5c6"}
```

`instance_id` is the libvirt domain UUID or the Hyper-V VM Id. It changes when
a VM is destroyed and created again under the same name. It is omitted for UTM
and Tart, which have no identifier Sailwright reads.

## Concurrent operations

`build`, `create`, `start`, `stop`, `destroy`, `provision`, `pull` and `push`
take an operating-system file lock in the app-data `locks/` directory. Two
operations on the same instance (`os/type/arch[/name]`), or any operation during an `all` or registry
transfer, do not run at the same time. Named instances have independent locks.
Creates also hold a shared read lock on their image, so independent clones can
proceed together while builds and image transfers wait. A second lock in the
cache directory also protects a shared cache used from different app-data
directories. Hyper-V serializes box
registration/import separately to protect the shared box. A busy operation fails immediately and
names the process holding the lock. Set `SAILWRIGHT_LOCK_TIMEOUT` to a Go
duration such as `30s` to wait instead.

Security metadata (`network_mode`, `network_detail`, `credentials_origin`) was
added within schema version 1 and is present in version 2:

| Field | Values and contract |
| --- | --- |
| `network_mode` | `nat`, `bridged`, or `unknown`. These are exposure categories: `nat` includes isolated/host-only networking, and `bridged` includes routing. Public or unclassified port forwards and unsupported backends can produce `unknown`. |
| `network_detail` | Human-readable inspection details. Do not parse this diagnostic text as a stable identifier. |
| `credentials_origin` | `legacy`, `user`, `none` (local host), or `unknown` (credential configuration unavailable). `user` is a source label, not proof of password strength or a password change inside the guest. |

For missing `create` targets these fields describe the planned deployment and
image credentials. Existing Create targets and other listings describe the
current target. An unavailable credential configuration does not remove targets
or fail the listing: its origin becomes `unknown`. Lifecycle guards still reject
invalid configuration before making changes.

Warnings go to stderr, separate from the JSON document on stdout. See
[Guest network security](./guest-security.md) for the policy and overrides.
