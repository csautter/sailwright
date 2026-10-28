# MacOS Setup Instructions
## playwright_win11_iso.py
This script uses Playwright to fetch the latest Windows 11 ISO download link from Microsoft's official download page.
### Prerequisites
- Python 3.10 or higher (the pinned Playwright no longer supports Python 3.9, which macOS ships as `/usr/bin/python3`)
- Playwright library
### Installation
1. **Install Python**: Ensure you have Python 3.10 or higher installed. `sailwright install` installs Homebrew `python@3.13`; to install it by hand:
   ```bash
   brew install python@3.13
   ```
2. **Set Up a Virtual Environment** (optional but recommended):
   ```bash
   python3.13 -m venv .venv
   source .venv/bin/activate
   ```
3. **Install the script requirements**:
   ```bash
   pip install -r requirements.txt
   python -m playwright install
   ```
### Running the Script
Activate your virtual environment if you created one:
```bash
source .venv/bin/activate
```
To get the latest Windows 11 ISO download link, run:
```bash
python playwright_win11_iso.py
```
To get the latest Windows 11 ISO arm version download link, use:
```bash
python playwright_win11_iso.py --arm
```
Or run the script with the `--headless false` flag to see the browser in action:
```bash
python playwright_win11_iso.py --headless false
```
This will output the latest Windows 11 ISO download link in the terminal.
Additionally, the script saves the download link to the managed cache directory:

- macOS default: `~/Library/Application Support/dev-alchemy/cache/windows/win11_amd64_iso_url.txt`
- macOS default: `~/Library/Application Support/dev-alchemy/cache/windows/win11_arm64_iso_url.txt`
- Session cookies: `~/Library/Application Support/dev-alchemy/cache/windows/playwright/cookies.json`
- Failure diagnostics: `~/Library/Application Support/dev-alchemy/cache/windows/playwright-diagnostics/`

Set `DEV_ALCHEMY_APP_DATA_DIR` to move the managed cache elsewhere.

If Microsoft serves a slow, blocked, or challenge page, the script now retries the initial navigation and writes a screenshot, HTML snapshot, and JSON metadata bundle under `playwright-diagnostics/` before failing.

### Download the ISO
CI does not use this path; it resolves ISOs through the versioned cache described in [docs/windows-iso-cache.md](../../docs/windows-iso-cache.md).

You can use `curl` or `wget` to download the ISO using the link saved in the file:
```bash
APP_DATA_DIR="${DEV_ALCHEMY_APP_DATA_DIR:-$HOME/Library/Application Support/dev-alchemy}"
CACHE_DIR="${DEV_ALCHEMY_CACHE_DIR:-$APP_DATA_DIR/cache}"
cd "$CACHE_DIR/windows/"
curl --progress-bar -o ../windows11/iso/win11_english_amd64.iso "$(cat ./win11_amd64_iso_url.txt)"
```
or for arm:
```bash
APP_DATA_DIR="${DEV_ALCHEMY_APP_DATA_DIR:-$HOME/Library/Application Support/dev-alchemy}"
CACHE_DIR="${DEV_ALCHEMY_CACHE_DIR:-$APP_DATA_DIR/cache}"
cd "$CACHE_DIR/windows/"
curl --progress-bar -o ../windows11/iso/win11_english_arm64.iso "$(cat ./win11_arm64_iso_url.txt)"
```
