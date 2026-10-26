# ADR 0003: OCI Build Artifact Registry Contract

## Status

Accepted

## Context

Dev Alchemy can push and pull completed VM build artifacts through OCI
registries. That makes the registry format a cross-command and cross-host
contract: `alchemy push`, `alchemy pull`, GitHub Actions publication, and local
artifact promotion must agree on media types, manifest annotations,
compatibility rules, and registry security behavior.

Without an explicit ADR, future changes could publish artifacts that pull into
the wrong local cache slot, require undocumented registry settings, or expand
GHCR publication beyond the artifacts the project is prepared to support.

## Decision

Dev Alchemy OCI build artifacts use OCI image manifest 1.1 artifacts with:

- artifact type `application/vnd.dev-alchemy.vm-build.v1`
- layer media type `application/vnd.dev-alchemy.vm-build.qcow2.v1` for
  `.qcow2` artifacts
- layer media type `application/vnd.dev-alchemy.vm-build.vagrant-box.v1` for
  `.box` artifacts
- layer media type `application/vnd.dev-alchemy.vm-build.artifact.v1` for other
  build artifact files

Every manifest must include enough annotations to identify both the artifact
and the VM target. The required Dev Alchemy target annotations are:

- `dev.alchemy.vm.os`
- `dev.alchemy.vm.type`
- `dev.alchemy.vm.arch`
- `dev.alchemy.vm.host_os`
- `dev.alchemy.vm.virtualization_engine`
- `dev.alchemy.vm.slug`

Manifests should also carry the standard OCI image annotations emitted by the
push path, including title, creation time, vendor, description, documentation,
source, authors, ref name, and component.

Pull validation must reject artifacts unless:

- the artifact type matches exactly
- each expected layer is present exactly once
- each layer has an OCI title annotation matching the expected local artifact
  name
- each layer media type matches the expected media type for that local artifact
- the VM target annotations match the requested OS, type, architecture, host OS,
  and virtualization engine

Compatible foreign pulls are allowed only as an explicit exception. A Linux
artifact may be pulled into a Darwin target, or a Darwin artifact may be pulled
into a Linux target, when the guest OS, guest type, architecture, expected layer
names, and layer media types match. The CLI must require confirmation for this
case, or `--yes` for non-interactive use.

`alchemy pull` must download into a temporary staging directory under the local
artifact root before promotion. Promotion must replace final artifact files only
after all expected staged files are present, back up existing files before
replacement, roll back partial replacements on failure, and clean successful
backups after promotion.

GitHub Container Registry publication is intentionally limited to Ubuntu build
artifacts produced by the Linux build workflow. Published references live under
`ghcr.io/<owner>/ubuntu-24` and use tags shaped as
`<type>-<arch>-linux-build`. Windows VM artifacts are not published to GHCR.

`sailwright build` may offer, interactively and with a default of "no", to
pull the published artifact matching the requested Ubuntu target on Linux and
Darwin hosts. Accepting that prompt counts as the confirmation for a compatible
foreign Darwin/Linux artifact, and the prompt says so on Darwin. This built-in
pull is anonymous and does not read Docker credentials. Because released CLIs
resolve `ghcr.io/csautter/ubuntu-24:<type>-<arch>-linux-build`, changing the
repository or tag shape is a breaking change for the offer: keep the old tags,
or ship a CLI update first. `test/workflowcheck` pins the publish job to the
CLI's reference format.

Registry authentication uses Docker credentials by default so `docker login`
works for authenticated registries. Command-specific credentials may override
that behavior, and callers may opt out of Docker credential lookup. For
registries using private trust roots, `--ca-file` is the preferred TLS option.
Plain HTTP and insecure TLS verification are explicit opt-in flags for local or
short-lived test registries only.

## Consequences

- OCI artifacts remain inspectable with standard registry tooling while keeping
  Dev Alchemy-specific target validation.
- Pulls fail closed when media types, annotations, or layer names drift.
- Cross-host artifact reuse stays possible for the Darwin/Linux cases that
  share a local artifact shape, but it remains visible to the user.
- GHCR publication has a narrow support boundary: Ubuntu Linux artifacts only,
  with no Windows artifact distribution contract.
