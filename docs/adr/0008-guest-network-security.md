# ADR 0008: Guest Network Exposure And Credential Guards

## Status

Accepted

## Context

Existing VM images contain documented development credentials, and deployment
networks can expose those images to the LAN. Operators need visibility and an
explicit decision before using unsafe credentials on an exposed or unknown
network. Provisioning environment variables describe how to connect; they do
not change accounts inside a guest.

## Decision

- Inspect network exposure and credential classification before the CLI performs
  Create, Start, or Provision operations. Stop, Destroy, and local provisioning
  remain outside this guest credential guard.
- Classify networks as `nat`, `bridged`, or `unknown`. These values describe
  exposure: isolated and host-only networks are included in `nat`, and routed
  networks are included in `bridged`. An exposed adapter dominates; otherwise
  an unknown adapter prevents a positive NAT classification.
- Account for additional entry points, including routed IPv6 under libvirt NAT
  and UTM port forwards. Ambiguous process arguments or unsupported Tart network
  backends are not evidence of NAT.
- Reject exposed or unknown networking with non-private credentials unless the
  operator explicitly accepts the risk through `--allow-insecure-credentials`
  or `security.allow_insecure_credentials` in the managed `config.yaml`.
  Overrides do not suppress warnings or bypass configuration parsing failures.
- Recognize documented defaults and passwords shorter than twelve Unicode
  characters. This classification is not a password-entropy guarantee.
- For existing guests, use provisioning's environment/`.env` resolution rules.
  The operator remains responsible for matching the configuration to the guest.
  For new guests, classify image credentials as `legacy` regardless of
  provisioning overrides.
- Show planned security metadata for missing Create targets and current metadata
  for existing targets. Listings remain available when credential metadata is
  unreadable: report its origin as `unknown` and warn without logging secrets.
  The mutating guard still rejects the same credential configuration error.
- Keep JSON schema 1 additive. The gateway's safe grammar does not admit the
  override; using it through the gateway requires host-side approval as in
  ADR 0005.

## Consequences

Previously accepted Bridge/unknown workflows with default credentials may now
fail before making changes. Existing UTM templates remain bridged, so operators
must review the network or explicitly accept the risk before creating those
guests. Guidance lives in [Guest network security](../guest-security.md).

Conservative inspection can classify an otherwise isolated VM as `unknown` when
its configuration cannot be established. This is preferable to silently
authorizing unsafe guest access. A `nat` result is not a firewall guarantee.

This decision covers visibility and guards only. Changing deployment defaults
to NAT and managing credentials per host remain separate future changes.
