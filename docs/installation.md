# Installation

Sailwright ships as two binaries per release: the `sailwright` CLI and the
host-side [Agent Gateway](./agent-gateway.md) `sailwright-gateway`. Pick one of
the ways below; all of them install the same release archives from
[GitHub Releases](https://github.com/csautter/sailwright/releases).

| Platform | Recommended | Fallback |
| --- | --- | --- |
| macOS | [Homebrew](#homebrew) | [Install script](#install-script) |
| Linux | [Install script](#install-script) | [Homebrew](#homebrew) |
| Windows | [Install script](#install-script) | [Manual install](#manual-install) |

After installing, set up the host dependencies with `sailwright install`.

## Homebrew

On macOS or Linux:

```bash
brew install csautter/sailwright/sailwright
brew install csautter/sailwright/sailwright-gateway   # optional
```

See [Homebrew Tap](./homebrew-tap.md) for how the tap is maintained.

## Install script

macOS / Linux:

```bash
curl -fsSL https://github.com/csautter/sailwright/releases/latest/download/install.sh | sh
```

Windows (PowerShell 5.1 or 7):

```powershell
irm https://github.com/csautter/sailwright/releases/latest/download/install.ps1 | iex
```

The URLs always serve the scripts from the latest release. The scripts:

1. detect the OS and architecture (`amd64` or `arm64`),
2. resolve the latest release tag from the GitHub releases page (no GitHub
   API call, so no rate limit),
3. download `sailwright_<version>_checksums.txt` and, if `cosign` is on your
   `PATH`, verify its keyless signature against the release workflow identity,
4. download the archives, check each SHA-256 against the checksums file and
   **abort without installing anything** on a mismatch,
5. copy the binaries into the install directory.

Nothing runs with `sudo` or admin rights, and no data is sent anywhere apart
from the downloads from `github.com`.

| | macOS / Linux | Windows |
| --- | --- | --- |
| Default directory | `~/.local/bin` | `%LOCALAPPDATA%\Programs\sailwright` |
| `PATH` | prints a hint if the directory is missing | adds the directory to the user `PATH` |

Rerun the command to update to the latest release.

### Options

| Setting | `install.sh` | `install.ps1` | Environment variable |
| --- | --- | --- | --- |
| Install a specific release | `--version v0.20.0` | `-Version v0.20.0` | `SAILWRIGHT_VERSION` |
| Install into another directory | `--install-dir <dir>` | `-InstallDir <dir>` | `SAILWRIGHT_INSTALL_DIR` |
| Skip `sailwright-gateway` | `--no-gateway` | `-NoGateway` | `SAILWRIGHT_NO_GATEWAY=1` |

When piping, pass flags to `install.sh` after `sh -s --`, and use the
environment variables for `install.ps1`:

```bash
curl -fsSL https://github.com/csautter/sailwright/releases/latest/download/install.sh | sh -s -- --no-gateway
```

```powershell
$env:SAILWRIGHT_NO_GATEWAY = '1'
irm https://github.com/csautter/sailwright/releases/latest/download/install.ps1 | iex
```

To read the script before running it, download it first:

```bash
curl -fsSLO https://github.com/csautter/sailwright/releases/latest/download/install.sh
less install.sh
sh install.sh --install-dir /opt/sailwright/bin
```

### Uninstall

Delete the two binaries from the install directory. On Windows, also remove
the directory from your user `PATH` (Settings → System → About → Advanced
system settings → Environment Variables).

## Manual install

Download `sailwright_<version>_<os>_<arch>` (`.tar.gz` on macOS/Linux, `.zip`
on Windows) and `sailwright_<version>_checksums.txt` from the release, verify
the archive, and extract it:

```bash
TAG="$(curl -fsSL https://api.github.com/repos/csautter/sailwright/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
VERSION="${TAG#v}"
curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_linux_amd64.tar.gz"
curl -fLO "https://github.com/csautter/sailwright/releases/download/${TAG}/sailwright_${VERSION}_checksums.txt"
grep "sailwright_${VERSION}_linux_amd64.tar.gz" "sailwright_${VERSION}_checksums.txt" | sed 's| dist/| |' | sha256sum -c -
tar -xzf "sailwright_${VERSION}_linux_amd64.tar.gz"
./sailwright --help
```

To verify the checksums file itself with cosign:

```bash
cosign verify-blob \
  --certificate "sailwright_${VERSION}_checksums.txt.pem" \
  --signature "sailwright_${VERSION}_checksums.txt.sig" \
  --certificate-identity "https://github.com/csautter/sailwright/.github/workflows/release-binaries.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  "sailwright_${VERSION}_checksums.txt"
```

The README's [Manual install](../README.md#manual-install) section has a
Windows example as well.
