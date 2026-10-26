# Security Policy

## Supported Versions

Sailwright is pre-1.0 (see [CHANGELOG.md](./CHANGELOG.md) for the current
version). Only the **latest published release** is supported with security
fixes; please upgrade before reporting an issue against an older version.

## Reporting a Vulnerability

Please do not open a public GitHub issue for security vulnerabilities.

Instead, report privately through one of:

- [GitHub Security Advisories](https://github.com/csautter/sailwright/security/advisories/new)
  for this repository (preferred)
- Email [cc@sautter.cc](mailto:cc@sautter.cc) with a description of the issue,
  affected version(s), and reproduction steps if available

You should receive an acknowledgment within a few business days. We'll work
with you to confirm the issue, prepare a fix, and agree on a disclosure
timeline before any public write-up.

## Verifying Release Downloads

Every release publishes a `sailwright_<version>_checksums.txt` file alongside
the platform archives on the
[GitHub Releases page](https://github.com/csautter/sailwright/releases),
generated with `sha256sum` over all published artifacts.

To verify a downloaded archive:

```bash
# Download the archive and the checksums file for the same release, e.g.:
curl -fLO "https://github.com/csautter/sailwright/releases/download/v0.17.0/sailwright_0.17.0_linux_amd64.tar.gz"
curl -fLO "https://github.com/csautter/sailwright/releases/download/v0.17.0/sailwright_0.17.0_checksums.txt"

# Verify (run from the directory containing both files):
sha256sum --ignore-missing -c sailwright_0.17.0_checksums.txt
```

A `sailwright_0.17.0_linux_amd64.tar.gz: OK` line confirms the archive matches
what was published in that release.

### Signature verification

Release checksums are also signed keylessly with
[cosign](https://docs.sigstore.dev/cosign/) via GitHub Actions OIDC, so you
can verify the checksums file itself came from this repository's release
workflow rather than trusting the download alone:

```bash
cosign verify-blob \
  --certificate-identity "https://github.com/csautter/sailwright/.github/workflows/release-binaries.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature "sailwright_0.17.0_checksums.txt.sig" \
  --certificate "sailwright_0.17.0_checksums.txt.pem" \
  "sailwright_0.17.0_checksums.txt"
```

Download the `.sig` and `.pem` files for the checksums file from the same
release page before running the command above.
