# Guest network security

`sailwright create`, `start`, and `provision` check guest network exposure and
configured credentials before changing the VM. Bridged or unknown networks with
non-private credentials are refused unless you explicitly accept the risk.
Warnings remain visible even when an override allows the command.

This also applies to an already running VM passed to `start`. `stop`, `destroy`,
and local-host provisioning are not blocked by this guest credential policy.

## Network exposure

Listings show `NET` and `CREDS` columns. The network values describe exposure
categories rather than the exact hypervisor topology:

| Value | Meaning |
| --- | --- |
| `nat` | Recognized NAT, isolated, or host-only networking without an identified external entry point. This is not a firewall or reachability guarantee. |
| `bridged` | Bridged or routed networking that can expose the guest to the LAN. Any such adapter determines the result for the VM. |
| `unknown` | Inspection failed, the configuration is ambiguous, or an external entry point cannot be classified safely. Unknown networking receives the same protection as bridged networking. |

libvirt NAT with IPv6 addresses is routed over IPv6 unless the network explicitly
enables `<nat ipv6="yes"/>`; Sailwright treats that combination as exposed.
See the [libvirt network format](https://libvirt.org/formatnetwork.html#connectivity).

UTM Emulated networking can publish guest services through port forwards. Only
explicit numeric loopback bindings, such as `127.0.0.1` or `::1`, retain the
`nat` classification. Wildcard, LAN, missing, empty, or unrecognized bindings
produce `unknown`. Both current `HostAddress` and older `hostAddress` keys are
recognized. See [UTM's port-forward argument generation](https://github.com/utmapp/UTM/blob/main/Configuration/UTMQemuConfiguration%2BArguments.swift).

For running Tart VMs, Sailwright inspects process arguments. External network
backends such as `--net-fd`, unsupported arguments, and ambiguous VM-name matches
produce `unknown`; the absence of `--net-bridged` alone does not establish NAT.
See [Tart's network options](https://github.com/cirruslabs/tart/blob/main/Sources/tart/Commands/Run.swift).

## Guest credentials

The check recognizes documented defaults (`P@ssw0rd!`, `admin`, `packer`, and
`vagrant`) and passwords shorter than twelve Unicode characters as non-private.
The length threshold is a screening rule, not a password-entropy guarantee.

| Credential origin | Meaning |
| --- | --- |
| `legacy` | The default provisioning password is being used, or a new VM will use the image's existing credentials. |
| `user` | A non-empty provisioning password was supplied through the environment or project `.env`. This label alone does not establish password strength. |
| `none` | The target is the local host, outside this guest policy. |
| `unknown` | A listing could not read or parse the credential configuration. The target remains visible and a warning explains the missing classification. |

For existing VMs, the check uses the same environment-before-`.env` precedence as
provisioning, with the engine-specific prefixes:

| Engine | Prefixes |
| --- | --- |
| UTM | `UTM_UBUNTU_`, `UTM_WINDOWS_` |
| Hyper-V | `HYPERV_UBUNTU_`, `HYPERV_WINDOWS_` |
| libvirt/QEMU | `LIBVIRT_UBUNTU_`, `LIBVIRT_WINDOWS_` |
| Tart | `TART_MACOS_` |

Append `ANSIBLE_USER` and `ANSIBLE_PASSWORD` to the appropriate prefix. An empty
or whitespace-only environment value falls back to `.env`, then to the image's
documented default. Changing these variables does **not** change any password
inside a VM. The operator must ensure the configured credentials describe the
guest and that default accounts are no longer usable before relying on them.

For a new VM, a provisioning override cannot change credentials baked into its
image. `create` therefore checks the planned network with `legacy` credentials.
`create list` shows this same planned state for missing targets; existing targets
show their current configuration. Other listings inspect the current target.
See [machine-readable listings](./machine-readable-listings.md) for the JSON
contract.

## Explicit risk acceptance

For a single operation that you have reviewed, pass the override to that command:

```bash
sailwright create ubuntu --type server --arch arm64 --allow-insecure-credentials
sailwright start ubuntu --type server --arch arm64 --allow-insecure-credentials
sailwright provision ubuntu --type server --arch arm64 --check --allow-insecure-credentials
```

Each command needs its own flag. To accept the risk persistently for these VM
operations, set the following in the managed config directory's `config.yaml`:

```yaml
security:
  allow_insecure_credentials: true
```

The setting defaults to `false`; a missing file keeps protection enabled. Remove
the setting or set it to `false` to restore protection. The file applies across
VM targets using that config directory, not just one host or guest. See
[config locations and overrides](./managed-application-data.md#config-location).

Unknown keys, invalid YAML, and multiple YAML documents cause an error. A flag
does not bypass an invalid config file. Unreadable or malformed credential
configuration still blocks `start` and `provision`, including with the override;
listings instead show credential origin `unknown`. Neither warning nor error
output includes credential values.

Through the Agent Gateway, the override is outside the safe command set and
requires host-side approval. It is not added automatically to typed operations.

## Migration from earlier versions

Commands that previously accepted an exposed guest with default credentials can
now exit with an error before the lifecycle action. Inspect `create list` or
`start list`, check the reported network details, and confirm guest credentials.
Where the guest password was changed, configure the matching provisioning
variables. Alternatively use an appropriate isolated/NAT network, or explicitly
accept the remaining exposure as described above.

The shipped UTM templates still use **bridged networking**. Their new-VM
`create` commands therefore require either a suitable template change or explicit
risk acceptance. Provisioning overrides alone do not make a new image private.
This release adds detection and guards; it does not change the default deployment
network or generate credentials per host.

QEMU image builds bind their SSH/WinRM port forwards to `127.0.0.1`. This build
setting is separate from the deployed VM's network configuration.
