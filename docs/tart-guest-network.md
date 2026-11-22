# Tart Guest Network: DNS and Readiness

An IPv4 address does not make a Tart macOS VM ready. After `create` and
`start`, and before every `provision` of a Tart VM (including one that is
already running), Sailwright checks from inside the guest:

1. **guest-access**: a command runs through the Tart guest agent
   (`tart exec`).
2. **dns-config**: applies `TART_MACOS_DNS_SERVERS` if you set it.
3. **system-dns**: the guest's system resolver resolves each check name.
4. **https**: `curl` reaches each check URL with normal certificate
   verification. Any HTTP status counts as reachable.

Retries are bounded (default window 90 seconds). On failure the command
fails and names the guest, the failed stage, the guest's resolvers with a
direct probe result, and a hint. Provisioning then runs no Ansible roles.
The VM keeps running so you can inspect it.

```text
guest network readiness for "tahoe-base-alchemy": failed at stage system-dns after 4 attempt(s) in 1m28s:
system resolver did not answer for github.com within 15s; guest resolvers: 192.168.64.1 (timeout);
hint: the guest uses the DHCP-provided resolver 192.168.64.1, which is the Tart NAT (vmnet) DNS
forwarder on the host, and it does not answer; set TART_MACOS_DNS_SERVERS ...
```

## Settings

All settings are environment variables. Gateway jobs only receive them when
they are listed in the gateway's `env_passthrough` (the gateway then redacts
their values in output).

| Variable | Default | Meaning |
| --- | --- | --- |
| `TART_MACOS_DNS_SERVERS` | unset | Unset: guest DNS is not touched. A comma-separated list of IP addresses: set these DNS servers on the guest's primary network service. `dhcp`: revert servers Sailwright set earlier. |
| `TART_MACOS_NETWORK_CHECK` | `full` | `full`: all stages. `offline`: guest access only; the result says `offline` and that DNS and HTTPS were **not** verified. `off`: no check; the result says `skipped`. |
| `TART_MACOS_NETWORK_CHECK_URLS` | `https://github.com/` | Comma-separated `https://` URLs for the HTTPS stage. |
| `TART_MACOS_NETWORK_CHECK_DNS_NAMES` | hosts of the check URLs | Names for the system-dns stage, for example an internal name behind split DNS. |
| `TART_MACOS_NETWORK_CHECK_TIMEOUT` | `90s` | Total retry window (Go duration, up to `30m`). |

Use `offline` for host-only or air-gapped work. `off` also suits images
without the Tart guest agent; network readiness is then not verified.

## Explicit DNS servers

Sailwright has no built-in resolver addresses. Pick a resolver the guest can
reach and that your network policy allows, for example the resolver your
network or VPN client documents:

```bash
export TART_MACOS_DNS_SERVERS=192.0.2.53
sailwright start macos --arch arm64
```

Sailwright sets the servers with `networksetup -setdnsservers` on the guest
service that holds the default route (normally `Ethernet`) and records them
in `/var/db/sailwright/tart-dns` inside the guest. The setting:

- persists in the guest across stop/start and guest reboots, so later starts
  need no variable;
- applies to new VMs on `create` and to existing VMs on the next `start` or
  `provision`;
- is idempotent: matching servers are left as they are;
- never replaces manual DNS servers that Sailwright did not set. Sailwright
  keeps them, logs a warning and checks the network with them.

Explicit servers replace the DHCP-provided resolver for every name in the
guest. If you rely on split DNS (internal domains that only a corporate
resolver answers), choose a resolver that serves those domains too, or leave
the variable unset and fix the host path instead. Sailwright does not change
host DNS, VPN or security-client settings.

When you move between networks (office, home, VPN), the resolver you set may
stop being reachable. The readiness check then fails at `system-dns` with
the configured resolver marked `timeout`; set a new value or revert to DHCP.

### Back to DHCP

```bash
TART_MACOS_DNS_SERVERS=dhcp sailwright start macos --arch arm64
```

This only reverts servers that Sailwright recorded. To reset manual servers
yourself inside the guest:

```bash
sudo networksetup -setdnsservers Ethernet Empty
```

## Diagnosing the NAT DNS path

With Tart's default NAT network the guest receives the host side of the
vmnet bridge (for example `192.168.64.1`) as router and DNS server through
DHCP. macOS Internet Sharing answers DNS there through a DNS proxy in
`mDNSResponder` that forwards to the host's resolvers. If the check reports
that resolver as `timeout`, compare from the host:

```bash
dig @192.168.64.1 +time=2 +tries=1 example.com   # NAT DNS forwarder
dig +time=2 +tries=1 example.com                 # host system resolver
netstat -anv -p udp | grep '\.53 '               # who holds UDP port 53
netstat -anv -p tcp | grep '\.53 .*LISTEN'       # who holds TCP port 53
```

A timeout on the forwarder while the host resolver answers means the host's
NAT DNS forwarder is broken, not the guest. Other software on the host that
binds port 53 (for example container or VM tools that forward guest DNS
ports to all host addresses) or VPN and security clients that take over DNS
can interfere with the forwarder. Bridged networking
(`TART_MACOS_BRIDGED_INTERFACE`) avoids the NAT forwarder entirely; the
guest then uses your LAN's DHCP resolver.

For HTTPS failures with curl exit codes such as 60, a TLS-inspecting proxy
is the usual cause: install its root CA in the guest trust store. Sailwright
never disables certificate verification.
