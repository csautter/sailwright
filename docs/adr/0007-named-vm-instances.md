# ADR 0007: Named VM Instances

## Status

Accepted

## Context

A VM image can back several independent local VMs. Existing installations must
remain addressable without migrating names, disks, or provider state. VM identity
must also support an optional OS version in the future.

## Decision

The logical identity is `(os, version, type, arch, name)`. Version is reserved for
future use; it is empty today. Image identity excludes the instance name. The CLI
uses `--name`, which has no existing VM selector conflict. An omitted name selects
the default instance. Explicit names contain 1–24 lowercase ASCII letters,
digits, or internal hyphens, start and end with a letter or digit, and cannot be
`default` or `all`.

The common provider name is
`<os>[-<version>][-<type>]-<arch>[-<name>]-dev-alchemy`. For example,
`ubuntu-server-amd64-work-dev-alchemy`. Parse known OS/type/architecture prefixes
and the fixed suffix, keeping the entire remainder as the name (including
hyphens). A future version is inserted immediately after the OS, never into the
instance name. Reserve a compact numeric version token, such as `2604`, so
`ubuntu-2604-server-amd64-work-dev-alchemy` remains unambiguous and fits host
limits. Version support and versioned image slugs are separate additions. Names
and image slugs without a version remain unchanged.

Default provider identities remain byte-for-byte compatible. Tart keeps its
legacy `tahoe-base-alchemy` default (and `TART_MACOS_VM_NAME` override); named Tart
instances use the common scheme. The override and an explicit name are mutually
exclusive. Default images, build paths, and image caches stay shared by all
instances. Provider disks, bundles, and Vagrant state use the provider identity.

The hypervisor is authoritative for existence and instance IDs. A versioned
app-data registry supplements discovery with creation and image metadata; it is
never required to discover an existing VM. Recreated VMs retain their logical
address but receive a new hypervisor instance ID and require new gateway grants.

Listings display the default instance as `default`; JSON uses an empty `name`
for that instance. Bulk stop/destroy continue to select only defaults unless
`--all-instances` is supplied. Operation locks include the instance name; image
preparation uses a shared image lock. Gateway targets accept an optional fourth
component, `<os>/<type>/<arch>[/<name>]`.

Named guest hostnames follow the provider name on Unix. Windows computer names
use `sw-` plus twelve hexadecimal digits from the logical identity hash to meet
the 15-character limit. Defaults retain their existing guest hostnames. Provider
MAC addresses and display endpoints are allocated per VM; build VNC ports remain
image-scoped. Hyper-V box registration/import and removal share one host-wide
lock, including callers using different app-data directories.

Named Linux clones initialize a persistent unique machine ID and DHCP identity
before guest access; restarting an existing clone preserves that identity.
QEMU/UTM use the guest agent and refresh network clients. Hyper-V Ubuntu builds
clear machine identity before export, and named creation requires a matching
completion marker recording that preparation. Older Hyper-V Ubuntu images must
be rebuilt before named creation because Vagrant needs networking before it
can configure the guest.

## Consequences

Default VMs need no migration. Named VMs can be discovered across app-data directories.
Guest hostnames and provider-assigned networking must allow concurrent instances.
Host-provider checks are required in addition to portable identity tests.
