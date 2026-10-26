# Homebrew Tap

Sailwright is published to the
[csautter/homebrew-sailwright](https://github.com/csautter/homebrew-sailwright)
tap with two formulae:

| Formula | Installs |
| --- | --- |
| `sailwright` | the `sailwright` CLI and its shell completions |
| `sailwright-gateway` | the host-side [Agent Gateway](./agent-gateway.md) and its shell completions |

```bash
brew install csautter/sailwright/sailwright
```

The formulae download the release archives for macOS and Linux (`amd64` and
`arm64`) from GitHub Releases and pin each one to the SHA-256 in the release's
`sailwright_<version>_checksums.txt`. Homebrew refuses to install an archive
whose checksum does not match. Windows is not covered by Homebrew.

## How the tap is updated

The formulae are generated, never edited by hand:

1. `release-binaries.yml` builds and publishes the release assets and the
   checksums file.
2. The `homebrew-tap` job downloads that checksums file, renders both formulae
   with [`scripts/homebrew/render-formula.sh`](../scripts/homebrew/render-formula.sh),
   and pushes them to `Formula/` in the tap repository.
3. The `homebrew-verify` job installs both formulae from the tap on macOS and
   Linux runners and runs `brew test`.

The `homebrew-tap` job refuses to publish a pre-release version or to move the
tap to a lower version than the one it already contains, so re-running an old
release does not downgrade users.

On pull requests, `homebrew-formula-check` runs the renderer's tests
(`scripts/homebrew/render-formula_test.sh`), renders the formulae from the PR
build artifacts, and checks them with `ruby -c`, `brew style`, and
`brew audit --strict`.

To render a formula locally from a published release:

```bash
curl -fLO "https://github.com/csautter/sailwright/releases/download/v<version>/sailwright_<version>_checksums.txt"
scripts/homebrew/render-formula.sh sailwright <version> sailwright_<version>_checksums.txt
```

## Before the first release

Do this once, before the first release that should update the tap. Until it
is done, releases still succeed; the `homebrew-tap` job only logs a warning
and skips the update.

- [ ] Create the **public** repository `csautter/homebrew-sailwright` with an
      initial commit (a `README.md` is enough). The `Formula/` directory is
      created by the first release. Homebrew finds the tap by this name, so
      do not rename it.
- [ ] Create a fine-grained personal access token with:
  - **Repository access:** only `csautter/homebrew-sailwright`
  - **Permissions:** Contents: Read and write
  - an expiry date you track; the tap update fails once it expires
- [ ] Store the token as the Actions secret `HOMEBREW_TAP_TOKEN` in
      `csautter/sailwright` (Settings, Secrets and variables, Actions).
- [ ] Publish the formulae: merge the next release PR, or run the
      **Release Binaries** workflow manually with the latest existing version
      (for example `v0.17.0`). The manual run rebuilds, re-uploads, and
      re-signs that release's assets and checksums, so the archives may differ
      from the originals.
- [ ] Check that the `homebrew-tap` job pushed a `sailwright <version>`
      commit to the tap and that both `homebrew-verify` jobs (macOS and
      Linux) are green.
- [ ] Try it on a real machine:

  ```bash
  brew install csautter/sailwright/sailwright
  sailwright --help
  ```

When the token is rotated, update the `HOMEBREW_TAP_TOKEN` secret; nothing
else changes.
