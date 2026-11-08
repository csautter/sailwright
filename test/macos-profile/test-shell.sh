#!/usr/bin/env bash
# Production functions are sourced dynamically; their state is shared with tests.
# shellcheck disable=SC1091,SC2030,SC2031,SC2034,SC2154,SC2317
# Match the production scripts: Bash 3.2 treats empty arrays as unset with nounset.
set -eo pipefail
cd "$(dirname "$0")/../.."
test_dir="$(mktemp -d /tmp/sw-shell.XXXXXX)"
trap 'rm -rf "$test_dir"' EXIT
real_python="$(command -v python3)"
original_path="$PATH"
export TMPDIR="$test_dir/var/folders/long-macos-per-user-temporary-directory/T"
mkdir -p "$TMPDIR"
mkdir -p "$test_dir/bin"
fail() { echo "FAIL: $*" >&2; exit 1; }
# Extract functions verbatim from production scripts, with no test hooks in builds.
"$real_python" test/macos-profile/extract-functions.py build/packer/windows/windows11-qemu.sh > "$test_dir/functions.sh"
source "$test_dir/functions.sh"
"$real_python" test/macos-profile/extract-functions.py scripts/macos/sailwright-install-dependencies.sh > "$test_dir/brew-functions.sh"
source "$test_dir/brew-functions.sh"
if [[ "${1:-}" != "--native" ]]; then
    cat > "$test_dir/bin/uname" <<'EOF'
#!/bin/sh
case "$1" in -s) echo Darwin;; -m) echo arm64;; *) exit 1;; esac
EOF
    cat > "$test_dir/bin/stat" <<EOF
#!/bin/sh
[ "\$1" = '-f%z' ] || exit 1
exec "$real_python" -c 'import os,sys; print(os.stat(sys.argv[1]).st_size)' "\$2"
EOF
fi
cat > "$test_dir/bin/sw_vers" <<'EOF'
#!/bin/sh
echo 26.6.2
EOF
cat > "$test_dir/bin/osascript" <<'EOF'
#!/bin/sh
echo 'unexpected UTM automation during shell tests' >&2
exit 1
EOF
# Old system Python must be rejected, even if it is first on PATH.
for interpreter in python3 python3.14 python3.12 python3.11 python3.10 python; do
    cat > "$test_dir/bin/$interpreter" <<EOF
#!/bin/sh
exec "$real_python" -c 'import sys; sys.version_info=(3,9,0); exec(sys.argv[1])' "\$2"
EOF
done
cat > "$test_dir/bin/python3.13" <<EOF
#!/bin/sh
exec "$real_python" -c 'import sys; sys.version_info=(3,13,18); exec(sys.argv[1])' "\$2"
EOF
cat > "$test_dir/bin/brew" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$BREW_TEST_LOG"
case "$1" in
list) printf '%s\n' "$BREW_TEST_VERSIONS";;
unpin|pin|upgrade|install|tap) :;;
*) exit 1;;
esac
EOF
chmod +x "$test_dir/bin/"*
export PATH="$test_dir/bin:$original_path"
[[ "$(detect_host_os)" == darwin ]] || fail 'Darwin detection'
[[ "$(detect_host_arch)" == arm64 ]] || fail 'Apple Silicon detection'
[[ "$(resolve_python_for_venv)" == python3.13 ]] || fail 'Python 3.9 must yield to supported Python'
# Model Homebrew outside PATH without creating files at system locations.
(
    eval "$(declare -f python_is_supported | sed '1s/python_is_supported/production_python_is_supported/')"
    # Called by the dynamically sourced resolve_python_for_venv function.
    # shellcheck disable=SC2329
    command() {
        if [[ "$1" == -v ]]; then
            [[ "$2" == /opt/homebrew/bin/python3 ]]
        else
            builtin command "$@"
        fi
    }
    # shellcheck disable=SC2329
    python_is_supported() {
        [[ "$1" == /opt/homebrew/bin/python3 ]] || return 1
        production_python_is_supported "$test_dir/bin/python3.13"
    }
    [[ "$(resolve_python_for_venv)" == /opt/homebrew/bin/python3 ]] || fail 'Homebrew Python outside PATH'
)
# Use a macOS-length output path, retaining the real home environment.
(
    profile_home='/Users/ci-user-with-a-long-name/Library/Developer/temporary-account'
    host_os=darwin
    app_data="$(default_app_data_dir)"
    [[ "$app_data" == "$HOME/Library/Application Support/dev-alchemy" ]] || fail 'app-data path'
    build_output_dir="$profile_home/Library/Application Support/dev-alchemy/cache/windows11/qemu-out-windows11-arm64"
    qmp_socket_dir=''
    create_qmp_socket_dir
    trap cleanup_qmp_socket_dir EXIT
    [[ "${#qmp_socket_path}" -le 104 ]] || fail 'QMP socket exceeds Darwin limit'
    # Bind it as well: a short string alone does not prove a usable socket.
    "$real_python" -c 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1]); s.close()' "$qmp_socket_path"
)
# Exercise BSD stat from the Ubuntu script (native on macOS, stubbed on Linux).
"$real_python" test/macos-profile/extract-functions.py build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh > "$test_dir/ubuntu-functions.sh"
source "$test_dir/ubuntu-functions.sh"
printf '12345' > "$test_dir/size"
[[ "$(file_size_bytes "$test_dir/size")" == 5 ]] || fail 'BSD file size'
create_qmp_socket_dir
[[ "${#qmp_socket_path}" -le 104 ]] || fail 'Ubuntu QMP socket exceeds Darwin limit'
cleanup_qmp_socket_dir
# Execute both production start-only isolation blocks with a long app-data path.
for script in build/packer/windows/windows11-qemu.sh build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh; do
    "$real_python" - "$script" > "$test_dir/start-only.sh" <<'PY'
from pathlib import Path
import re,sys
text=Path(sys.argv[1]).read_text()
match=re.search(r'(?ms)^if is_truthy "\$packer_start_only"; then\n.*?^fi',text)
assert match
print(match[0])
PY
    arch=arm64
    ubuntu_type=server
    packer_start_only=true
    effective_cache_dir="$test_dir/Library/Application Support/dev-alchemy/cache"
    source "$test_dir/start-only.sh"
    [[ "${#build_output_dir}" -lt 50 ]] || fail 'start-only output path too long'
    rm -rf "$start_only_cache_dir"
done
export BREW_TEST_LOG="$test_dir/brew.log"
export BREW_TEST_VERSIONS='qemu 9.2.4 11.1.2_1'
[[ "$(brew_installed_version qemu)" == 11.1.2_1 ]] || fail 'highest installed formula version'
brew_version_matches 11.1.2_1 11.1.2 || fail 'formula revision accepted'
brew_version_matches 11.1.3 11.1.2 || fail 'newer formula accepted'
if brew_version_matches 9.2.4 11.1.2; then fail 'old formula accepted'; fi
export BREW_TEST_VERSIONS='qemu 11.1.2_1'
brew_install QEMU true qemu 11.1.2 > "$test_dir/install.log"
grep -qx 'pin qemu' "$BREW_TEST_LOG" || fail 'satisfied formula is pinned'
: > "$BREW_TEST_LOG"
export BREW_TEST_VERSIONS='python@3.13 3.13.18'
brew_install Python true python@3.13 3.13.18 '' --no-pin > "$test_dir/install.log"
grep -qx 'unpin python@3.13' "$BREW_TEST_LOG" || fail 'shared Python remains unpinned'
if grep -qx 'pin python@3.13' "$BREW_TEST_LOG"; then fail 'Python was pinned'; fi
: > "$BREW_TEST_LOG"
export BREW_TEST_VERSIONS='qemu 9.2.4'
brew_install QEMU true qemu 11.1.2 > "$test_dir/install.log" 2>&1
grep -qx 'upgrade qemu' "$BREW_TEST_LOG" || fail 'old formula must upgrade'
grep -q 'does not satisfy' "$test_dir/install.log" || fail 'unavailable version must warn'
if grep -qx 'pin qemu' "$BREW_TEST_LOG"; then fail 'unsatisfied formula was pinned'; fi
export BREW_TEST_VERSIONS='utm 4.7.5,105'
[[ "$(brew_installed_version utm --cask)" == '4.7.5,105' ]] || fail 'cask version parsing'
brew_version_matches 4.7.5,105 4.7.5 --cask || fail 'cask build accepted'
# Exercise the actual installer call as well as its helper: dropping Python
# from the installation list must fail even if version parsing still works.
: > "$BREW_TEST_LOG"
export BREW_TEST_VERSIONS='python@3.13 3.13.18'
"$real_python" - > "$test_dir/python-install.sh" <<'PYTHON'
from pathlib import Path
text=Path('scripts/macos/sailwright-install-dependencies.sh').read_text()
for line in text.splitlines():
    if line.startswith('PYTHON_VERSION=') or line.startswith('brew_install "Python'):
        print(line)
PYTHON
source "$test_dir/python-install.sh"
grep -qx 'unpin python@3.13' "$BREW_TEST_LOG" || fail 'installer must include unpinned supported Python'
echo 'macOS shell tests passed.'
