#!/usr/bin/env bash
# Phases run through the dispatcher at the end; ps output is filtered by path, not name.
# shellcheck disable=SC2317,SC2329,SC2009
# Release test phases for macOS and Linux hosts. See test/release/README.md.
#
#   bash test/release/unix.sh <phase> [<phase>...]
#
# Each phase is one approval: read it once, run it, then read
# test/release/out/<host>-<date>/<phase>/summary.md. Settings come from
# test/release/release.env (copy release.env.example). Phases never print
# guest passwords and never touch VMs that existed before the run.
set -u

KIT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=test/release/lib/rt.sh disable=SC1091 # CI runs ShellCheck without -x
. "$KIT_DIR/lib/rt.sh"
# Invocation settings take precedence over release.env, including explicit empty
# values (the Tart pass clears the UTM pull reference and credential override).
rt_setting_overrides=()
for rt_setting in "${!RT_@}"; do
	rt_setting_overrides+=("$rt_setting=${!rt_setting}")
done
if [ -f "$KIT_DIR/release.env" ]; then
	# shellcheck disable=SC1091
	. "$KIT_DIR/release.env"
fi
for rt_setting in "${rt_setting_overrides[@]}"; do
	# shellcheck disable=SC2163 # entries are NAME=value assignments
	export "$rt_setting"
done
unset rt_setting rt_setting_overrides

HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$(uname -m)" in
x86_64 | amd64) HOST_ARCH=amd64 ;;
arm64 | aarch64) HOST_ARCH=arm64 ;;
*) HOST_ARCH="$(uname -m)" ;;
esac

RT_PLAN="${RT_PLAN:-unspecified}"
RT_TARGET="${RT_TARGET:-ubuntu/server/$HOST_ARCH}"
RT_VERSION="${RT_VERSION:-local-$(git -C "$RT_REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
RT_BIN_DIR="${RT_BIN_DIR:-$RT_REPO_ROOT/dist/$HOST_OS-$HOST_ARCH}"
BIN="$RT_BIN_DIR/sailwright"
GW="$RT_BIN_DIR/sailwright-gateway"
RT_RUN_DIR="${RT_RUN_DIR:-$RT_KIT_DIR/out/$(rt__host)-$(date +%Y%m%d)}"
# Isolated app data keeps the operator's images, caches and VM bundles out of
# reach. RT_APPDATA=default uses the normal Sailwright directories instead.
RT_APPDATA="${RT_APPDATA:-$RT_RUN_DIR/appdata}"
RT_GATEWAY_PORT="${RT_GATEWAY_PORT:-7399}"
export RT_RUN_DIR

T_OS="${RT_TARGET%%/*}"
T_REST="${RT_TARGET#*/}"
T_TYPE="${T_REST%%/*}"
T_ARCH="${T_REST#*/}"
T_ARCH="${T_ARCH%%/*}"
TARGET_ARGS=("$T_OS" --arch "$T_ARCH")
if [ -n "$T_TYPE" ]; then TARGET_ARGS+=(--type "$T_TYPE"); fi

# sw runs the CLI under test with the selected app data directory.
sw() {
	if [ "$RT_APPDATA" = default ]; then
		"$BIN" "$@"
	else
		mkdir -p "$RT_APPDATA"
		DEV_ALCHEMY_APP_DATA_DIR="$RT_APPDATA" "$BIN" "$@"
	fi
}

appdata_env() {
	if [ "$RT_APPDATA" != default ]; then printf 'DEV_ALCHEMY_APP_DATA_DIR=%s\n' "$RT_APPDATA"; fi
}
# APPDATA_ENV holds the same as an `env` argument list (possibly empty).
APPDATA_ENV=()
if [ "$RT_APPDATA" != default ]; then APPDATA_ENV=("DEV_ALCHEMY_APP_DATA_DIR=$RT_APPDATA"); fi

# The shipped UTM templates use bridged networking; their lifecycle needs an explicit
# risk acceptance (docs/guest-security.md). INSECURE is empty or holds that flag.
INSECURE=()
if [ "${RT_ALLOW_INSECURE_CREDENTIALS:-}" = 1 ]; then INSECURE=(--allow-insecure-credentials); fi

need_bin() {
	if [ ! -x "$BIN" ] || [ ! -x "$GW" ]; then
		echo "Missing $BIN or $GW; run the kit phase first (or set RT_BIN_DIR)." >&2
		exit 3
	fi
}

target_state() { # COMMAND FIELD -> value for RT_TARGET from `<command> list --json`
	sw "$1" list --json 2>/dev/null | python3 -I -c '
import json, sys
os_, typ, arch, field = sys.argv[1:5]
for t in json.load(sys.stdin)["targets"]:
    if t["os"] == os_ and (t.get("type") or "") == typ and t["arch"] == arch:
        print(t["fields"].get(field, "")); break' "$T_OS" "$T_TYPE" "$T_ARCH" "$2"
}

# SELF re-enters this script to run the CLI with the selected app data (`sw`).
SELF=(bash "$KIT_DIR/unix.sh" __sw)

appdata_root() {
	if [ "$RT_APPDATA" != default ]; then
		printf '%s\n' "$RT_APPDATA"
	elif [ "$HOST_OS" = darwin ]; then
		printf '%s\n' "$HOME/Library/Application Support/dev-alchemy"
	else
		printf '%s\n' "${XDG_DATA_HOME:-$HOME/.local/share}/dev-alchemy"
	fi
}

processes_matching() { # PATTERN -> matching processes, excluding this shell's tools
	rt_processes_matching "$1"
}

e2e_env() { # common environment for direct E2E runs
	printf '%s\n' "SAILWRIGHT_E2E_BIN=$BIN" "SAILWRIGHT_E2E_TARGET=$RT_TARGET"
	appdata_env
	if [ -n "${RT_PULL_REF:-}" ]; then printf 'SAILWRIGHT_E2E_PULL_REF=%s\n' "$RT_PULL_REF"; fi
	if [ "${RT_ALLOW_INSECURE_CREDENTIALS:-}" = 1 ]; then printf 'SAILWRIGHT_E2E_ALLOW_INSECURE_CREDENTIALS=1\n'; fi
}

run_e2e() { # ID DESC RUN [extra ENV=VALUE...]
	local id="$1" desc="$2" run="$3" line
	shift 3
	local -a envs=()
	while IFS= read -r line; do envs+=("$line"); done < <(e2e_env)
	rt_e2e "$id" "$desc" "$run" "${envs[@]}" "$@"
}

# ---------------------------------------------------------------- preflight
phase_preflight() {
	rt_init preflight
	rt_check PRE-01 "host identity" -- uname -a
	if [ "$HOST_OS" = darwin ]; then
		rt_check PRE-02 "macOS version" -- sw_vers
		rt_check PRE-03 "hardware" -- sh -c 'sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize; sysctl kern.hv_support'
	else
		rt_check PRE-02 "Linux distribution" -- cat /etc/os-release
		rt_check PRE-03 "hardware and KVM" -- sh -c 'nproc; free -g; ls -l /dev/kvm 2>&1; grep -cE "vmx|svm" /proc/cpuinfo'
	fi
	rt_check PRE-04 "free disk for checkout and run directory" -- df -h "$RT_REPO_ROOT" "$RT_RUN_DIR"
	rt_check PRE-05 "checkout commit" -- git -C "$RT_REPO_ROOT" log -1 --format='%H %d %s'
	# The candidate is the unreleased origin/main. A checkout of a branch based on it is
	# accepted only when it differs from main in test/ and docs/ alone (e.g. the kit).
	rt_check PRE-06 "fetch origin/main" --timeout 120 -- git -C "$RT_REPO_ROOT" fetch --quiet origin main
	rt_check PRE-06b "checkout contains the current origin/main" -- git -C "$RT_REPO_ROOT" merge-base --is-ancestor origin/main HEAD
	rt_check PRE-06c "product code equals origin/main (only test/ and docs/ may differ)" --nomatch '.' -- \
		git -C "$RT_REPO_ROOT" diff --name-only origin/main HEAD -- . ':(exclude)test' ':(exclude)docs'
	rt_check PRE-06f "no uncommitted product changes (the binary is built from the working tree)" --nomatch '.' -- \
		git -C "$RT_REPO_ROOT" status --porcelain -- . ':(exclude)test' ':(exclude)docs'
	rt_check PRE-06d "origin/main is unreleased (commits since the latest tag)" --match '-[1-9][0-9]*-g[0-9a-f]+$' -- \
		git -C "$RT_REPO_ROOT" describe --tags --long origin/main
	if [ -n "${RT_EXPECT_COMMIT:-}" ]; then
		rt_check PRE-06e "origin/main is still the commit all plans of this round test" --match "^${RT_EXPECT_COMMIT}" -- git -C "$RT_REPO_ROOT" rev-parse origin/main
	else
		rt_note "RT_EXPECT_COMMIT not set: testing origin/main at $(git -C "$RT_REPO_ROOT" rev-parse --short origin/main 2>/dev/null)"
	fi
	local tool
	for tool in go git python3 packer qemu-img virsh virt-install utmctl tart ansible ansible-playbook sshpass vagrant cosign brew; do
		if command -v "$tool" >/dev/null 2>&1; then
			rt_check "PRE-T-$tool" "tool $tool" --exit-any -- sh -c "command -v $tool; $tool --version 2>&1 | head -3 || $tool version 2>&1 | head -3"
		else
			rt_skip "PRE-T-$tool" "tool $tool" "not installed"
		fi
	done
	if command -v ansible >/dev/null 2>&1; then
		rt_check PRE-07 "ansible-core is at least 2.19" --match 'core 2\.(19|[2-9][0-9])' -- ansible --version
	fi
	rt_check PRE-08 "no gateway of the operator is affected (informational)" --exit-any -- sh -c 'ps ax -o pid=,command= | grep -F sailwright-gateway | grep -v grep || echo none'
	if [ -x "$BIN" ]; then
		rt_check PRE-09 "binary under test" -- sh -c "shasum -a 256 '$BIN' '$GW' 2>/dev/null || sha256sum '$BIN' '$GW'; '$BIN' version"
		local mode
		for mode in default isolated; do
			rt_check "PRE-10-$mode" "no managed VM exists ($mode app data)" \
				--json 'all(t["fields"].get("state") == "missing" for t in d["targets"])' -- \
				sh -c "if [ $mode = isolated ] && [ '$RT_APPDATA' != default ]; then export DEV_ALCHEMY_APP_DATA_DIR='$RT_APPDATA'; fi; '$BIN' destroy list --json 2>/dev/null"
		done
	else
		rt_skip PRE-09 "binary under test" "run the kit phase first"
	fi
	if [ "${RT_DISPOSABLE:-}" = 1 ]; then rt_pass PRE-11 "operator declared this machine disposable" "RT_DISPOSABLE=1"; else rt_note "RT_DISPOSABLE is not set: host-modifying phases will refuse to run"; fi
	rt_summary
}

# ---------------------------------------------------------------- kit
phase_kit() {
	rt_init kit
	rt_check KIT-01 "build CLI $RT_VERSION" --timeout 1800 -- make -C "$RT_REPO_ROOT" build-cli-local VERSION="$RT_VERSION"
	rt_check KIT-02 "build gateway $RT_VERSION" --timeout 1800 -- make -C "$RT_REPO_ROOT" build-gateway-local VERSION="$RT_VERSION"
	rt_check KIT-03 "binaries report the version" --match "$RT_VERSION" -- sh -c "'$BIN' --version; '$GW' --version"
	rt_check KIT-04 "unit tests" --timeout 3600 -- go -C "$RT_REPO_ROOT" test ./cmd/... ./internal/... ./pkg/... -count=1
	rt_check KIT-05 "race tests: builds, gateway, operation locks, OCI, self-update" --timeout 3600 -- \
		go -C "$RT_REPO_ROOT" test -race ./pkg/build/... ./internal/gateway/... ./internal/operationlock/... ./internal/selfupdate/... ./pkg/oci/... -count=1
	rt_check KIT-06 "E2E runner self-tests (fake CLI)" --timeout 1800 -- go -C "$RT_REPO_ROOT" test ./test/e2e/... -count=1
	rt_check KIT-07 "self-update fixture tests (real install scripts, local release server)" --timeout 1800 -- \
		sh -c "cd '$RT_REPO_ROOT' && python3 -I -m unittest discover -s test/selfupdate -v"
	rt_check KIT-08 "installer and dependency tests" --timeout 1800 -- \
		sh -c "cd '$RT_REPO_ROOT' && python3 -I -m unittest discover -s test/install -v && python3 -I -m unittest discover -s test/dependencies -v"
	rt_check KIT-09 "local provisioning helper tests" --timeout 1800 -- \
		sh -c "cd '$RT_REPO_ROOT' && python3 -I -m unittest discover -s test/localprovision/scripts -v"
	rt_check KIT-10 "binary checksums" -- sh -c "shasum -a 256 '$BIN' '$GW' 2>/dev/null || sha256sum '$BIN' '$GW'"
	rt_check KIT-11 "release kit helper regressions" --timeout 120 -- \
		python3 -I -m unittest discover -s "$RT_KIT_DIR/tests" -v
	rt_summary
}

# ---------------------------------------------------------------- cli (read-only)
phase_cli() {
	need_bin
	rt_init cli
	local cfg="$RT_PHASE_DIR/config" gws="$RT_PHASE_DIR/gateway-state"
	rm -rf "$cfg" "$gws"
	mkdir -p "$cfg"
	run_e2e CLI-01 "E2E smoke and cli-readonly scenarios" 'TestScenarios/(smoke|cli-readonly)$'
	rt_check CLI-02 "--version prints the build version" --match "$RT_VERSION" -- "$BIN" --version
	rt_check CLI-03 "development build refuses self-update with a clear message" --exit 1 --match 'development builds cannot self-update' -- "$BIN" update --check
	rt_check CLI-04 "auto-check can be enabled in an isolated config" --match 'Automatic update checks: true' -- \
		sh -c "DEV_ALCHEMY_CONFIG_DIR='$cfg' '$BIN' update --auto-check on && DEV_ALCHEMY_CONFIG_DIR='$cfg' '$BIN' version"
	rt_check CLI-05 "interval below 15m is rejected" --exit-nonzero --match '15m' -- env DEV_ALCHEMY_CONFIG_DIR="$cfg" "$BIN" update --auto-check-interval 5m
	rt_check CLI-06 "interval is stored as a duration string" --json 'd.get("interval") == "1h0m0s" or d.get("interval") == "1h"' -- \
		sh -c "DEV_ALCHEMY_CONFIG_DIR='$cfg' '$BIN' update --auto-check-interval 1h >/dev/null && cat '$cfg/update.json'"
	printf '{not json' >"$cfg/update.json"
	rt_check CLI-07 "invalid update.json is reported by version" --match 'update\.json|[Ii]nvalid' -- env DEV_ALCHEMY_CONFIG_DIR="$cfg" "$BIN" version
	rt_check CLI-08 "auto-check off repairs invalid settings" --match 'Automatic update checks: false' -- \
		sh -c "DEV_ALCHEMY_CONFIG_DIR='$cfg' '$BIN' update --auto-check off && DEV_ALCHEMY_CONFIG_DIR='$cfg' '$BIN' version"
	rt_check CLI-09 "update source override outside loopback is refused" --exit-nonzero -- \
		env SAILWRIGHT_UPDATE_BASE_URL=https://example.com DEV_ALCHEMY_CONFIG_DIR="$cfg" "$BIN" update --check
	printf 'security:\n  allow_insecure_credentials: maybe\n' >"$cfg/config.yaml"
	rt_check CLI-10 "invalid config.yaml blocks start even with the override (missing VM, nothing changes)" --exit-nonzero --match 'config\.yaml|allow_insecure_credentials' -- \
		env DEV_ALCHEMY_CONFIG_DIR="$cfg" DEV_ALCHEMY_APP_DATA_DIR="$RT_PHASE_DIR/appdata" "$BIN" start "${TARGET_ARGS[@]}" --allow-insecure-credentials
	rm -f "$cfg/config.yaml"
	rt_check CLI-11 "build listing shows NET and CREDS columns" --match 'NET' --match 'CREDS' -- "$BIN" build list
	rt_check CLI-12 "build help documents headless without VNC viewer" --match 'do not open a VNC viewer' -- "$BIN" build --help
	rt_check CLI-13 "create/start/provision accept --allow-insecure-credentials" --match 'allow-insecure-credentials' -- sh -c "'$BIN' create --help; '$BIN' start --help; '$BIN' provision --help"
	rt_check CLI-14 "gateway token is created owner-only" --match '^-rw-------' -- \
		sh -c "'$GW' --state-dir '$gws' token >/dev/null && ls -l '$gws/token'"
	rt_check CLI-15 "connect --check without a gateway fails clearly" --exit-nonzero -- \
		env SAILWRIGHT_GATEWAY_DIR="$RT_PHASE_DIR/no-gateway" SAILWRIGHT_GATEWAY_URL="unix://$RT_PHASE_DIR/no-gateway/gateway.sock" "$GW" connect --check
	rt_check CLI-16 "config sources list" -- "$BIN" config sources list
	rt_check CLI-17 "provision catalog lists roles" --match '[Rr]ole|[Tt]ool' -- "$BIN" provision catalog
	rt_summary
}

# ---------------------------------------------------------------- selfupdate
phase_selfupdate() {
	need_bin
	rt_init selfupdate
	local base="https://github.com/csautter/sailwright/releases"
	if [ -n "${RT_RELEASE_TAG:-}" ]; then
		local dir="$RT_PHASE_DIR/release-install"
		rm -rf "$dir"
		mkdir -p "$dir"
		rt_check SU-01 "install script installs $RT_RELEASE_TAG into a private directory" --timeout 600 -- \
			sh -c "curl -fsSL '$base/download/$RT_RELEASE_TAG/install.sh' | sh -s -- --version '$RT_RELEASE_TAG' --install-dir '$dir'"
		rt_check SU-02 "installed release reports its version" --match "${RT_RELEASE_TAG#v}" -- "$dir/sailwright" --version
		rt_check SU-03 "version shows script installation and settings" --match 'Installation:' --match 'Signature verification:' -- \
			env DEV_ALCHEMY_CONFIG_DIR="$RT_PHASE_DIR/config" "$dir/sailwright" version
		rt_check SU-04 "update --check against GitHub: 0 up to date or 2 newer" --exit-any --match 'is up to date|→' -- \
			env DEV_ALCHEMY_CONFIG_DIR="$RT_PHASE_DIR/config" "$dir/sailwright" update --check
	else
		rt_skip SU-01 "install released version" "RT_RELEASE_TAG not set (pre-release run)"
	fi
	if [ -n "${RT_UPDATE_FROM:-}" ]; then
		local old="$RT_PHASE_DIR/update-from"
		rm -rf "$old"
		mkdir -p "$old"
		rt_check SU-10 "install $RT_UPDATE_FROM as update source" --timeout 600 -- \
			sh -c "curl -fsSL '$base/download/$RT_UPDATE_FROM/install.sh' | sh -s -- --version '$RT_UPDATE_FROM' --install-dir '$old'"
		rt_check SU-11 "update --check reports an available update (exit 2)" --exit 2 -- env DEV_ALCHEMY_CONFIG_DIR="$RT_PHASE_DIR/config" "$old/sailwright" update --check
		if ! command -v cosign >/dev/null 2>&1; then
			rt_check SU-12 "--require-signature fails without cosign and changes nothing" --exit-nonzero --match 'cosign' -- \
				env DEV_ALCHEMY_CONFIG_DIR="$RT_PHASE_DIR/config" "$old/sailwright" update --yes --require-signature
			rt_check SU-13 "binary unchanged after refused update" --match "${RT_UPDATE_FROM#v}" -- "$old/sailwright" --version
		else
			rt_skip SU-12 "--require-signature without cosign" "cosign is installed"
		fi
		rt_check SU-14 "update --yes installs the latest release and the gateway" --timeout 600 --match 'Installed Sailwright' --match 'Gateway updated|cosign|checksums' -- \
			env DEV_ALCHEMY_CONFIG_DIR="$RT_PHASE_DIR/config" "$old/sailwright" update --yes
		rt_check SU-15 "updated CLI and gateway report the new version" --nomatch "${RT_UPDATE_FROM#v}\$" -- sh -c "'$old/sailwright' --version; '$old/sailwright-gateway' --version"
		rt_check SU-16 "update --check is current afterwards" --exit 0 -- env DEV_ALCHEMY_CONFIG_DIR="$RT_PHASE_DIR/config" "$old/sailwright" update --check
		rt_check SU-17 "no leftover .old backups" --exit-any --nomatch '\.old$' -- ls -a "$old"
	else
		rt_skip SU-10 "real update from an older release" "RT_UPDATE_FROM not set; needs a release that already has 'update' (>= v0.27.0)"
	fi
	if [ "$HOST_OS" = darwin ] && command -v brew >/dev/null 2>&1 && brew list --formula sailwright >/dev/null 2>&1; then
		rt_check SU-20 "Homebrew installation refuses self-update" --exit-nonzero --match '[Bb]rew' -- "$(brew --prefix)/bin/sailwright" update --check
	else
		rt_skip SU-20 "Homebrew installation refuses self-update" "sailwright is not installed through Homebrew here"
	fi
	rt_summary
}

# ---------------------------------------------------------------- install (disposable VM)
phase_install() {
	rt_require_disposable
	need_bin
	rt_init install
	local tag="${RT_RELEASE_TAG:-}"
	if [ -z "$tag" ]; then
		tag="$(git -C "$RT_REPO_ROOT" describe --tags --abbrev=0 2>/dev/null)"
		rt_note "no RT_RELEASE_TAG: the checkout's install.sh installs the latest existing release $tag"
	fi
	rt_check INS-01 "checkout install.sh installs $tag into ~/.local/bin" --timeout 600 -- sh "$RT_REPO_ROOT/scripts/install.sh" --version "$tag"
	# Releases before v0.27.0 have no --version flag; --help only proves the binary starts.
	local probe=--help
	[ -n "${RT_RELEASE_TAG:-}" ] && probe=--version
	# shellcheck disable=SC2016 # expanded by the inner shell, not here
	rt_check INS-02 "installed binaries run ($probe)" -- sh -c '"$HOME/.local/bin/sailwright" "$1" && "$HOME/.local/bin/sailwright-gateway" "$1"' sh "$probe"
	rt_check INS-03 "sailwright install (host dependencies)" --timeout 5400 -- "$BIN" install
	# shellcheck disable=SC2016 # expanded by the inner shell, not here
	rt_check INS-04 "ansible-core >= 2.19 after install" --match 'core 2\.(19|[2-9][0-9])' -- sh -c 'PATH="$HOME/.local/bin:$PATH" ansible --version'
	rt_check INS-05 "second install is idempotent" --timeout 5400 -- "$BIN" install
	if [ "$HOST_OS" = darwin ]; then
		# shellcheck disable=SC2016 # expanded by the inner shell, not here
		rt_check INS-06 "Homebrew tap installs sailwright and the gateway" --timeout 1800 -- \
			sh -c 'brew install csautter/sailwright/sailwright csautter/sailwright/sailwright-gateway && "$(brew --prefix)/bin/sailwright" "$1" && "$(brew --prefix)/bin/sailwright-gateway" "$1"' sh "$probe"
	fi
	rt_summary
}

# ---------------------------------------------------------------- provision-local (disposable VM)
recap_check() { # ID DESC PHASE(check|apply|idempotency) ARGS...
	local id="$1" desc="$2" kind="$3"
	shift 3
	local log="$RT_PHASE_DIR/logs/$id.run.log"
	# shellcheck disable=SC2016 # expanded by the inner shell, not here
	rt_check "$id" "$desc" --timeout 10800 -- sh -c '
		log="$1"; kind="$2"; shift 2
		"$@" >"$log" 2>&1; rc=$?
		# shellcheck disable=SC2016 # expanded by the inner shell, not here
		python3 -I "'"$RT_REPO_ROOT"'/test/localprovision/scripts/check_recap.py" "$log" "$kind" || exit 1
		exit "$rc"' sh "$log" "$kind" "$@"
}

phase_provision_local() {
	rt_require_disposable
	need_bin
	rt_init provision-local
	local playbook="${RT_PLAYBOOK:-$RT_REPO_ROOT/playbooks/setup.yml}"
	recap_check PL-01 "full bundled setup in check mode" check "$BIN" provision local --check --yes --playbook "$playbook"
	recap_check PL-02 "full bundled setup" apply "$BIN" provision local --yes --playbook "$playbook"
	recap_check PL-03 "second run changes nothing" idempotency "$BIN" provision local --yes --playbook "$playbook"
	rt_manual PL-04 "interactive run without --yes, with sudo password" \
		"In a real terminal on this machine: bash test/release/unix.sh provision-interactive  (types the sudo password at the prompt; results land in the provision-interactive phase)."
	rt_summary
}

phase_provision_interactive() {
	need_bin
	if [ ! -t 0 ]; then
		echo "Run this phase yourself in a terminal: it asks for your sudo password." >&2
		exit 3
	fi
	rt_init provision-interactive
	local playbook="${RT_PLAYBOOK:-$RT_REPO_ROOT/playbooks/setup.yml}" log
	# The bundled setup contains the brew and java roles: test fresh installs first.
	if [ "$HOST_OS" = darwin ] && [ "$HOST_ARCH" = amd64 ] && [ "${RT_BREW_FRESH:-}" = 1 ]; then
		log="$RT_PHASE_DIR/logs/PI-05.run.log"
		"$BIN" provision local --role brew -K 2>&1 | tee "$log"
		rt_check PI-05 "brew role on Intel without Homebrew" -- python3 -I "$RT_REPO_ROOT/test/localprovision/scripts/check_recap.py" "$log" apply
		rt_check PI-06 "Homebrew lives in /usr/local/Homebrew, brew is linked" -- sh -c 'test -d /usr/local/Homebrew && test -L /usr/local/bin/brew && ls -ld /usr/local/Homebrew /usr/local/bin/brew'
		rt_check PI-07 "/usr/local itself and unrelated content stay root-owned" --match '^root' -- stat -f '%Su %N' /usr/local /usr/local/include /usr/local/lib
	else
		rt_skip PI-05 "brew role on Intel without Homebrew" "needs an Intel Mac without Homebrew and RT_BREW_FRESH=1"
	fi
	if [ "${RT_JAVA_FRESH:-}" = 1 ]; then
		log="$RT_PHASE_DIR/logs/PI-02.run.log"
		echo ">> PI-02: java role on a host without Temurin."
		"$BIN" provision local --role java -K 2>&1 | tee "$log"
		rt_check PI-02 "java role with sudo password" -- python3 -I "$RT_REPO_ROOT/test/localprovision/scripts/check_recap.py" "$log" apply
		if [ "$HOST_OS" = darwin ]; then
			rt_check PI-03 "JDKs 8, 11, 17, 21 and 24 installed" --match '1\.8' --match '"?11\.' --match '17\.' --match '21\.' --match '24' -- /usr/libexec/java_home -V
		else
			rt_check PI-03 "JDK alternatives registered" --match 'java' -- update-alternatives --list java
		fi
		log="$RT_PHASE_DIR/logs/PI-04.run.log"
		"$BIN" provision local --role java -K 2>&1 | tee "$log"
		rt_check PI-04 "java role second run changes nothing" -- python3 -I "$RT_REPO_ROOT/test/localprovision/scripts/check_recap.py" "$log" idempotency
	else
		rt_skip PI-02 "java role on a host without Temurin" "set RT_JAVA_FRESH=1 on a host without Temurin"
	fi
	log="$RT_PHASE_DIR/logs/PI-01.run.log"
	echo ">> PI-01: full bundled run without --yes; enter your sudo password when asked."
	"$BIN" provision local -K --playbook "$playbook" 2>&1 | tee "$log"
	rt_check PI-01 "interactive full run with -K" -- python3 -I "$RT_REPO_ROOT/test/localprovision/scripts/check_recap.py" "$log" apply
	rt_summary
}

# ---------------------------------------------------------------- netclass (disposable Linux VM)
# libvirt NAT with IPv6 is routed unless <nat ipv6="yes"/> is set (275d299). Only
# libvirtd is needed, no KVM: create list reports the planned network of a missing VM.
phase_netclass() {
	rt_require_disposable
	need_bin
	rt_init netclass
	if [ "$HOST_OS" != linux ]; then rt_skip NC-00 "libvirt network classification" "Linux only"; rt_summary; return; fi
	local uri="${RT_LIBVIRT_URI:-qemu:///system}" orig="$RT_PHASE_DIR/default-network.xml" edited="$RT_PHASE_DIR/edited-network.xml"
	local mode_expr='[t for t in d["targets"] if t["os"]=="'"$T_OS"'" and (t.get("type") or "")=="'"$T_TYPE"'" and t["arch"]=="'"$T_ARCH"'"][0]'
	rt_check NC-01 "target VM does not exist" --match '^missing$' -- echo "$(target_state destroy state)"
	virsh --connect "$uri" net-dumpxml default >"$orig" || { rt_fail NC-02 "read the default network" "virsh net-dumpxml default failed"; rt_summary; return; }
	redefine() {
		virsh --connect "$uri" net-destroy default >/dev/null 2>&1
		virsh --connect "$uri" net-define "$1" >/dev/null && virsh --connect "$uri" net-start default >/dev/null
	}
	edit() { # MODE: ipv6 | ipv6-nat
		python3 -I - "$orig" "$edited" "$1" <<'PY'
import sys, xml.etree.ElementTree as ET
src, dst, mode = sys.argv[1:4]
tree = ET.parse(src); net = tree.getroot()
for ip in net.findall("ip"):
    if ip.get("family") == "ipv6":
        net.remove(ip)
ET.SubElement(net, "ip", {"family": "ipv6", "address": "fd42:5157::1", "prefix": "64"})
forward = net.find("forward")
if mode == "ipv6-nat":
    nat = forward.find("nat")
    if nat is None:
        nat = ET.SubElement(forward, "nat")
    nat.set("ipv6", "yes")
tree.write(dst)
PY
	}
	rt_check NC-02 "baseline: planned network is nat" --json "${mode_expr}[\"network_mode\"] == \"nat\"" -- sw create list --json
	edit ipv6 && redefine "$edited"
	rt_check NC-03 "NAT network with IPv6 but no <nat ipv6=yes/> is not nat" --json "${mode_expr}[\"network_mode\"] in (\"unknown\", \"bridged\")" -- sw create list --json
	rt_check NC-04 "listing explains the routed IPv6" --match '[Ii][Pp]v6' -- sw create list
	edit ipv6-nat && redefine "$edited"
	rt_check NC-05 "with <nat ipv6=yes/> the network is nat again" --json "${mode_expr}[\"network_mode\"] == \"nat\"" -- sw create list --json
	redefine "$orig"
	rt_check NC-06 "original default network restored" --json "${mode_expr}[\"network_mode\"] == \"nat\"" -- sw create list --json
	rt_summary
}

# ---------------------------------------------------------------- interrupt
phase_interrupt() {
	need_bin
	rt_init interrupt
	local data="$RT_PHASE_DIR/appdata" pty="$RT_KIT_DIR/lib/ptyctl.py" left
	rm -rf "$data"
	mkdir -p "$data"
	local -a build=(env DEV_ALCHEMY_APP_DATA_DIR="$data" "$BIN" build "${TARGET_ARGS[@]}" --headless --no-prebuilt)
	state() { DEV_ALCHEMY_APP_DATA_DIR="$data" "$BIN" build list --json 2>/dev/null | python3 -I -c '
import json, sys
os_, typ, arch = sys.argv[1:4]
for t in json.load(sys.stdin)["targets"]:
    if t["os"] == os_ and (t.get("type") or "") == typ and t["arch"] == arch: print(t["fields"]["build"])' "$T_OS" "$T_TYPE" "$T_ARCH"; }

	rt_check BI-01 "Ctrl+C during a dependency download stops within 30 s" --exit-nonzero \
		--json 'd["seconds_after_last_intr"] < 30' --match 'Received' -- \
		python3 -I "$pty" --log "$RT_PHASE_DIR/logs/BI-01.pty.log" --step 'wait:(?i)downloading:600' --step sleep:5 --step intr --step exit:45 -- "${build[@]}"
	rt_check BI-02 "cancelled download leaves no partial file" --nomatch '.' -- find "$data" -name '*.part'
	rt_check BI-02b "artifact not reported complete" --match '^(missing|incomplete)$' -- echo "$(state)"
	left="$(processes_matching "$data")"
	if [ -z "$left" ]; then rt_pass BI-03 "no build processes left after Ctrl+C"; else rt_fail BI-03 "no build processes left after Ctrl+C" "$left"; fi

	rt_check BI-04 "build all --parallel 2: one Ctrl+C during downloads stops everything" --timeout 1000 --exit-nonzero \
		--json 'd["seconds_after_last_intr"] < 45' --nomatch '==> .*: Starting|Build .* finished' -- \
		python3 -I "$pty" --log "$RT_PHASE_DIR/logs/BI-04.pty.log" --step 'wait:(?i)downloading:900' --step sleep:5 --step intr --step exit:60 -- \
		env DEV_ALCHEMY_APP_DATA_DIR="$data" "$BIN" build all --parallel 2 --headless --no-prebuilt
	left="$(processes_matching "$data")"
	if [ -z "$left" ]; then rt_pass BI-05 "no packer/qemu/ffmpeg/vncsnapshot left after build all"; else rt_fail BI-05 "no processes left after build all" "$left"; fi

	if [ "${RT_SKIP_PACKER_INTERRUPT:-}" = 1 ]; then
		rt_skip BI-06 "Ctrl+C while Packer runs" "RT_SKIP_PACKER_INTERRUPT=1"
	else
		# Let downloads finish (they are cached for the later steps), then interrupt Packer.
		rt_check BI-06 "Ctrl+C while Packer runs: stop, optional video, exit" --timeout 5700 --exit-nonzero \
			--json 'd["seconds_after_last_intr"] < 120' --match 'stopping all builds' -- \
			python3 -I "$pty" --log "$RT_PHASE_DIR/logs/BI-06.pty.log" --step 'wait:==> :5400' --step sleep:90 --step intr --step exit:180 -- "${build[@]}"
		rt_check BI-07 "interrupted build is not reported complete" --match '^(missing|incomplete)$' -- echo "$(state)"
		left="$(processes_matching "$data")"
		if [ -z "$left" ]; then rt_pass BI-08 "no build processes left after interrupting Packer"; else rt_fail BI-08 "no build processes left" "$left"; fi

		rt_check BI-09 "second Ctrl+C skips VNC post-processing, third exits" --timeout 2100 --exit-any \
			--json 'd["seconds_after_last_intr"] < 20' -- \
			python3 -I "$pty" --log "$RT_PHASE_DIR/logs/BI-09.pty.log" --step 'wait:==> :1800' --step sleep:90 --step intr \
			--step 'wait:Press Ctrl\+C again:60' --step intr --step 'wait:(?i)skipping (VNC video )?post-processing:60' --step intr --step exit:20 -- "${build[@]}"
		if ! grep -q 'Press Ctrl+C again' "$RT_PHASE_DIR/logs/BI-09.pty.log" 2>/dev/null; then
			rt_note "BI-09: no post-processing prompt seen; VNC recording may be unavailable on this host (check the log)"
		fi
		left="$(processes_matching "$data")"
		if [ -z "$left" ]; then rt_pass BI-10 "no processes left after the third Ctrl+C"; else rt_fail BI-10 "no processes left after the third Ctrl+C" "$left"; fi

		# kill -9 of sailwright itself: children are orphaned (known limitation), state must stay correct.
		local log="$RT_PHASE_DIR/logs/BI-11.run.log" pid waited=0
		"${build[@]}" >"$log" 2>&1 </dev/null &
		pid=$!
		while ! grep -q '==> ' "$log" 2>/dev/null && [ "$waited" -lt 1800 ]; do sleep 5; waited=$((waited + 5)); done
		sleep 30
		kill -9 "$pid" 2>/dev/null
		wait "$pid" 2>/dev/null
		rt_check BI-11 "kill -9 during a build: artifact not reported complete" --match '^(missing|incomplete)$' -- echo "$(state)"
		left="$(processes_matching "$data")"
		rt_note "BI-12: processes orphaned by kill -9 (expected, documented limitation): ${left:-none}"
		processes_matching "$data" | awk '{print $2}' | sort -u | while read -r pgid; do kill -TERM "-$pgid" 2>/dev/null; done
		sleep 15
		left="$(processes_matching "$data")"
		if [ -z "$left" ]; then rt_pass BI-12 "orphans cleaned up by the kit"; else rt_fail BI-12 "orphans cleaned up by the kit" "$left"; fi
	fi
	if [ "$HOST_OS" = darwin ]; then
		# The Windows 11 ARM64 unattended ISO is built with hdiutil and xorriso and needs no
		# sudo, so no password prompt is expected here. The check keeps the single-build
		# case: the build owns the terminal and one Ctrl+C stops it.
		rt_manual BI-13 "single build owns the terminal and stops on Ctrl+C (no sudo prompt expected)" \
			"In a terminal: $BIN build windows11 --arch $HOST_ARCH --headless ; wait for '==> ', then Ctrl+C. Expected: no sudo password prompt (the ISO step needs no sudo); Ctrl+C stops the build. Report anything that asks for a password, or a build that keeps running."
	fi
	rt_summary
}

# ---------------------------------------------------------------- build (physical host)
phase_build() {
	need_bin
	rt_init build
	local sampler="$RT_PHASE_DIR/logs/BLD-02.samples.log" spid
	: >"$sampler"
	(
		while :; do
			{
				date '+%H:%M:%S'
				ps ax -o command= | grep -E 'qemu-system|Screen Sharing|vncviewer|remote-viewer|vncsnapshot|ffmpeg' | grep -v grep
			} >>"$sampler"
			sleep 10
		done
	) &
	spid=$!
	rt_check BLD-01 "full headless build of $RT_TARGET" --timeout 21600 -- sw build "${TARGET_ARGS[@]}" --headless --no-prebuilt
	rt_stop_sampler "$spid"
	rt_check BLD-02 "headless: no display window or VNC viewer" --nomatch 'Screen Sharing|vncviewer|remote-viewer|-display (gtk|sdl|cocoa)' --match 'qemu-system' -- cat "$sampler"
	if grep -qE 'vncsnapshot|ffmpeg' "$sampler"; then rt_pass BLD-03 "VNC recording still ran in headless mode"; else rt_skip BLD-03 "VNC recording ran" "no recorder process sampled (Windows hosts or recording disabled)"; fi
	rt_check BLD-04 "artifact is complete" --match '^exists$' -- echo "$(target_state build build)"
	rt_check BLD-05 "completion marker written" --match 'sailwright-complete' -- find "$(appdata_root)" -name '*.sailwright-complete'
	rt_check BLD-05b "no pending marker left" --nomatch '.' -- find "$(appdata_root)" -name '*.sailwright-pending*'
	rt_check BLD-06 "unchanged second build is skipped" --timeout 900 -- "${SELF[@]}" build "${TARGET_ARGS[@]}" --headless --no-prebuilt
	rt_check BLD-06b "artifact still complete after the skipped build" --match '^exists$' -- echo "$(target_state build build)"
	if [ -n "${RT_PULL_REF:-}" ]; then
		local data="$RT_PHASE_DIR/pull-appdata"
		rm -rf "$data"
		rt_check BLD-07 "interrupted OCI pull" --exit-nonzero -- \
			python3 -I "$RT_KIT_DIR/lib/ptyctl.py" --log "$RT_PHASE_DIR/logs/BLD-07.pty.log" --step 'wait:Downloading OCI artifact:600' --step sleep:20 --step intr --step exit:60 -- \
			env DEV_ALCHEMY_APP_DATA_DIR="$data" "$BIN" pull "$RT_PULL_REF" --os "$T_OS" --type "${T_TYPE:-server}" --arch "$T_ARCH" --yes
		rt_check BLD-08 "repeated pull completes and verifies the digest" --timeout 7200 -- \
			env DEV_ALCHEMY_APP_DATA_DIR="$data" "$BIN" pull "$RT_PULL_REF" --os "$T_OS" --type "${T_TYPE:-server}" --arch "$T_ARCH" --yes
		rt_check BLD-09 "pulled artifact is complete" --match '"artifact":"exists"' -- env DEV_ALCHEMY_APP_DATA_DIR="$data" "$BIN" pull list --json
	else
		rt_skip BLD-07 "interrupted and resumed OCI pull" "RT_PULL_REF not set"
	fi
	if [ "${RT_WITH_WINDOWS:-}" = 1 ]; then
		local cache
		cache="$(appdata_root)/cache"
		mkdir -p "$cache"
		printf 'stale' >"$cache/virtio-win.iso"
		rt_check BLD-10 "windows11 build: dependencies, then Ctrl+C at Packer start" --exit-any -- \
			python3 -I "$RT_KIT_DIR/lib/ptyctl.py" --log "$RT_PHASE_DIR/logs/BLD-10.pty.log" --step 'wait:==> :10800' --step intr --step exit:180 -- \
			"${SELF[@]}" build windows11 --arch "$HOST_ARCH" --headless
		rt_check BLD-11 "virtio-win is cached under a versioned name; stale unversioned file removed" --match 'virtio-win-[0-9]' --nomatch 'virtio-win\.iso$' -- \
			find "$(appdata_root)" -name 'virtio-win*' -print
		rt_manual BLD-12 "full windows11 build (hours)" "Optional: $BIN build windows11 --arch $HOST_ARCH --headless with DEV_ALCHEMY_APP_DATA_DIR=$RT_APPDATA; expected exists + marker."
	else
		rt_skip BLD-10 "windows11 dependency cache migration" "RT_WITH_WINDOWS not set"
	fi
	rt_summary
}

# ---------------------------------------------------------------- vm (physical host)
phase_vm() {
	need_bin
	rt_init vm
	run_e2e VM-01 "E2E vm-access-cli: identity, info, two create/destroy cycles" 'TestScenarios/vm-access-cli$'
	run_e2e VM-02 "E2E vm-lifecycle: create, start, provision x3, stop, destroy" 'TestScenarios/(vm-lifecycle|destroy-idempotent)$'
	if [ "${RT_GUARD_TARGET:-}" != "" ]; then
		rt_e2e VM-03 "E2E guest-security-guard on $RT_GUARD_TARGET" 'TestScenarios/guest-security-guard$' \
			"SAILWRIGHT_E2E_BIN=$BIN" "SAILWRIGHT_E2E_TARGET=$RT_GUARD_TARGET" ${APPDATA_ENV[@]+"${APPDATA_ENV[@]}"}
	else
		rt_skip VM-03 "guest-security-guard" "RT_GUARD_TARGET not set (needs a target with bridged/unknown planned network)"
	fi

	# Instance identity across recreation and against the hypervisor.
	local id1 id2
	rt_check VM-10 "create for identity checks" --timeout 3600 -- "${SELF[@]}" create "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"}
	rt_check VM-10b "start for identity checks" --timeout 3600 -- "${SELF[@]}" start "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"}
	id1="$(sw vm identity "${TARGET_ARGS[@]}" 2>/dev/null | python3 -I -c 'import json,sys; print(json.load(sys.stdin).get("instance_id",""))')"
	if [ -n "$id1" ]; then rt_pass VM-11 "instance id reported" "$id1"; else rt_fail VM-11 "instance id reported" "empty instance_id"; fi
	case "$HOST_OS" in
	linux)
		local dom="${T_OS}-${T_TYPE:+$T_TYPE-}${T_ARCH}-dev-alchemy"
		rt_check VM-12 "identity equals virsh domuuid" --match "^${id1:-none}\$" -- virsh --connect "${RT_LIBVIRT_URI:-qemu:///system}" domuuid "$dom"
		rt_check VM-13 "running guest uses NAT networking" --match 'Network: nat' -- sw vm info "${TARGET_ARGS[@]}"
		;;
	darwin)
		if [ "$T_OS" = macos ]; then
			rt_check VM-12 "Tart identity is a filesystem fingerprint" --match '^tart:[0-9a-f]{64}$' -- echo "$id1"
		elif command -v utmctl >/dev/null 2>&1; then
			rt_check VM-12 "identity is the registered UTM UUID" --match "${id1:-none}" -- \
				bash -o pipefail -c 'utmctl list | tr "[:upper:]" "[:lower:]"'
		fi
		;;
	esac
	# Lock contention between two CLI processes.
	local stoplog="$RT_PHASE_DIR/logs/VM-20.stop.log" spid
	# Run the CLI as the direct background child (env execs it), so $! is the pid in
	# its holder record; a backgrounded `sw` would report its subshell instead.
	env ${APPDATA_ENV[@]+"${APPDATA_ENV[@]}"} "$BIN" stop "${TARGET_ARGS[@]}" >"$stoplog" 2>&1 </dev/null &
	spid=$!
	# Start the competing command only once this stop holds the target lock (its
	# holder record is in the lock file). A stop that finishes before that, as on
	# Tart, proves nothing either way.
	if rt_wait_lock_holder "$(appdata_root)/locks" "$spid"; then
		rt_check VM-20 "concurrent start is refused while stop holds the lock" --exit-nonzero --match 'resource busy' --match 'held by pid [0-9]+' -- \
			env SAILWRIGHT_LOCK_TIMEOUT=0s "${SELF[@]}" start "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"}
	else
		rt_skip VM-20 "concurrent start is refused while stop holds the lock" "INCONCLUSIVE: stop finished before its lock was observed; lock contention not tested"
	fi
	wait "$spid"
	rt_check VM-21 "stop finished normally" --match "." -- cat "$stoplog"
	sw start "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"} >"$RT_PHASE_DIR/logs/VM-22.start.log" 2>&1 </dev/null &
	spid=$!
	sleep 2
	rt_check VM-22 "a second operation waits for the lock and then succeeds" --timeout 1800 -- env SAILWRIGHT_LOCK_TIMEOUT=300s "${SELF[@]}" stop "${TARGET_ARGS[@]}"
	wait "$spid"
	rt_check VM-23 "destroy" --timeout 1800 -- "${SELF[@]}" destroy "${TARGET_ARGS[@]}"
	rt_check VM-24 "recreate" --timeout 3600 -- "${SELF[@]}" create "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"}
	id2="$(sw vm identity "${TARGET_ARGS[@]}" 2>/dev/null | python3 -I -c 'import json,sys; print(json.load(sys.stdin).get("instance_id",""))')"
	if [ -n "$id2" ] && [ "$id2" != "$id1" ]; then rt_pass VM-25 "instance id changed after recreate" "$id1 -> $id2"; else rt_fail VM-25 "instance id changed after recreate" "'$id1' -> '$id2'"; fi
	if [ "$HOST_OS" = darwin ] && [ "$T_OS" != macos ] && command -v utmctl >/dev/null 2>&1; then
		rt_check VM-26 "UTM holds no stale registration of the old bundle" --nomatch "${id1:-none}" -- \
			bash -o pipefail -c 'utmctl list | tr "[:upper:]" "[:lower:]"'
	fi
	rt_check VM-27 "cleanup destroy" --timeout 1800 -- "${SELF[@]}" destroy "${TARGET_ARGS[@]}"
	rt_check VM-28 "target is missing at the end" --match '^missing$' -- echo "$(target_state destroy state)"
	if [ "$HOST_OS" = linux ] && [ "${RT_WITH_WINDOWS:-}" = 1 ]; then
		local win=(windows11 --arch "$HOST_ARCH") wdom="windows11-$HOST_ARCH-dev-alchemy"
		rt_check VM-40 "build windows11 (cached when the build phase ran it)" --timeout 21600 -- "${SELF[@]}" build "${win[@]}" --headless
		rt_check VM-41 "create windows11 libvirt guest" --timeout 3600 -- "${SELF[@]}" create "${win[@]}"
		rt_check VM-42 "Hyper-V enlightenments in the domain (virt-install --xml path)" \
			--match "relaxed state='on'" --match "vpindex state='on'" --match "stimer state='on'" --match "tlbflush state='on'" --match "ipi state='on'" --match "hypervclock present='yes'" -- \
			virsh --connect "${RT_LIBVIRT_URI:-qemu:///system}" dumpxml "$wdom"
		rt_check VM-43 "windows11 guest starts and reports WinRM" --timeout 3600 --match 'Protocol: winrm' -- sh -c "$(printf '%q ' "${SELF[@]}") start ${win[*]} && $(printf '%q ' "${SELF[@]}") vm info ${win[*]}"
		rt_check VM-44 "destroy windows11" --timeout 1800 -- "${SELF[@]}" destroy "${win[@]}"
	else
		rt_skip VM-40 "windows11 libvirt guest with Hyper-V enlightenments" "Linux only, RT_WITH_WINDOWS=1"
	fi
	if [ "${RT_FULL_MATRIX:-}" = 1 ]; then
		local matrix
		case "$HOST_OS" in darwin) matrix=macos ;; *) matrix=linux ;; esac
		rt_e2e VM-30 "E2E vm-lifecycle-$matrix: every VM option of this host" "TestScenarios/vm-lifecycle-$matrix" \
			"SAILWRIGHT_E2E_BIN=$BIN" ${APPDATA_ENV[@]+"${APPDATA_ENV[@]}"} ${RT_PULL_REF:+"SAILWRIGHT_E2E_PULL_REF=$RT_PULL_REF"}
	else
		rt_skip VM-30 "full per-host lifecycle matrix" "RT_FULL_MATRIX not set"
	fi
	rt_summary
}

# ---------------------------------------------------------------- gateway (physical host)
GW_STATE=""
GW_AGENT=""
GW_ROOT=""
GW_PID=""
gateway_start() { # MODE(--bypass-approvals|--no-approvals) LOG
	mkdir -p "$GW_STATE" "$GW_AGENT"
	cat >"$GW_STATE/gateway.yaml" <<EOF
state_isolation: shared
agent_dir: $GW_AGENT
listen: 127.0.0.1:$RT_GATEWAY_PORT
EOF
	# Without a terminal the binary cannot be confirmed interactively, so pin its hash.
	local pin=()
	if [ "$1" != --bypass-approvals ]; then pin=(--binary-sha256 "$(shasum -a 256 "$BIN" | awk '{print $1}')"); fi
	if [ "$RT_APPDATA" = default ]; then
		"$GW" --state-dir "$GW_STATE" serve --config "$GW_STATE/gateway.yaml" --binary "$BIN" ${pin[@]+"${pin[@]}"} "$1" >"$2" 2>&1 </dev/null &
	else
		DEV_ALCHEMY_APP_DATA_DIR="$RT_APPDATA" "$GW" --state-dir "$GW_STATE" serve --config "$GW_STATE/gateway.yaml" --binary "$BIN" ${pin[@]+"${pin[@]}"} "$1" >"$2" 2>&1 </dev/null &
	fi
	GW_PID=$!
	local waited=0
	until SAILWRIGHT_GATEWAY_DIR="$GW_AGENT" "$GW" connect --check >/dev/null 2>&1; do
		sleep 1
		waited=$((waited + 1))
		if [ "$waited" -gt 60 ] || ! kill -0 "$GW_PID" 2>/dev/null; then return 1; fi
	done
}

# The gateway refuses a state dir inside any git checkout, and the agent dir must
# not overlap it. Use a short private directory under $HOME (Unix socket paths
# are length-limited) and copy the evidence into the results folder afterwards.
gateway_dirs() {
	GW_ROOT="$(mktemp -d "$HOME/.sailwright-rt-gw.XXXXXX")"
	GW_STATE="$GW_ROOT/state"
	GW_AGENT="$GW_ROOT/agent"
}

gateway_finish() {
	gateway_stop
	if [ -n "$GW_ROOT" ] && [ -d "$GW_ROOT" ]; then
		mkdir -p "$RT_PHASE_DIR/gateway-evidence"
		cp -R "$GW_STATE/." "$RT_PHASE_DIR/gateway-evidence/" 2>/dev/null || true
		case "$GW_ROOT" in "$HOME"/.sailwright-rt-gw.*) rm -rf "$GW_ROOT" ;; esac
		GW_ROOT=""
	fi
}

gateway_stop() {
	if [ -n "$GW_PID" ] && kill -0 "$GW_PID" 2>/dev/null; then
		kill -INT "$GW_PID"
		wait "$GW_PID" 2>/dev/null
	fi
	GW_PID=""
}

phase_gateway() {
	need_bin
	rt_init gateway
	gateway_dirs
	trap gateway_finish EXIT
	export SAILWRIGHT_GATEWAY_DIR="$GW_AGENT"
	local log="$RT_PHASE_DIR/logs/GW-serve-bypass.log"
	if gateway_start --bypass-approvals "$log"; then rt_pass GW-01 "gateway serves with --bypass-approvals (isolated state dir)"; else rt_fail GW-01 "gateway starts" "see logs/GW-serve-bypass.log"; rt_summary; return; fi
	rt_check GW-02 "banner shows bypassed approvals" --match 'approvals:[[:space:]]+BYPASSED' -- cat "$log"
	rt_check GW-03 "connect --check finds the gateway" -- "$GW" connect --check
	rt_check GW-04 "audit records gateway_started with the binary hash" --match '"gateway_started"' --match '[0-9a-f]{64}' -- cat "$GW_STATE/audit.jsonl"
	rt_e2e GW-05 "E2E smoke, cli-readonly, destroy-idempotent through the gateway" 'TestScenarios/(smoke|cli-readonly|destroy-idempotent)$' \
		SAILWRIGHT_E2E_EXECUTOR=gateway "SAILWRIGHT_E2E_TARGET=$RT_TARGET"
	rt_check GW-06 "TestVMAccess: create, vm_info, SSH, SCP, port refusal, cleanup" --timeout 3600 --match '--- PASS: TestVMAccess' -- \
		env SAILWRIGHT_VM_ACCESS_E2E=1 SAILWRIGHT_E2E_TARGET="$RT_TARGET" SAILWRIGHT_E2E_INSECURE="${RT_ALLOW_INSECURE_CREDENTIALS:-}" PATH="$RT_BIN_DIR:$PATH" \
		go -C "$RT_REPO_ROOT" test -tags e2e ./test/e2e -run '^TestVMAccess$' -count=1 -timeout 55m -v
	rt_check GW-07 "audit: vm_info and paired tunnels with traffic both ways" -- python3 -I -c '
import json, sys
events = [json.loads(line) for line in open(sys.argv[1]) if line.strip()]
names = [e.get("event") for e in events]
assert "vm_info" in names or any("vm_info" in json.dumps(e) for e in events), "no vm_info event"
opened, closed = names.count("tunnel_opened"), names.count("tunnel_closed")
assert opened >= 1 and opened == closed, f"tunnels opened={opened} closed={closed}"
moved = [e for e in events if e.get("event") == "tunnel_closed" and (e.get("params") or {}).get("bytes_to_vm", 0) > 0 and (e.get("params") or {}).get("bytes_from_vm", 0) > 0]
assert moved, "no tunnel with traffic both ways"
print(f"tunnels={opened}, with traffic={len(moved)}")' "$GW_STATE/audit.jsonl"
	rt_check GW-08 "ownership record is empty after cleanup" --json 'not d.get("vms")' -- sh -c "cat '$GW_STATE/owned_vms.json' 2>/dev/null || echo '{}'"
	rt_check GW-09 "TestVMOwnership: CLI-created VM, approval, recreate invalidates access, lock contention" --timeout 3600 --match '--- PASS: TestVMOwnership' -- \
		env SAILWRIGHT_VM_OWNERSHIP_E2E=1 SAILWRIGHT_E2E_TARGET="$RT_TARGET" SAILWRIGHT_E2E_BIN="$BIN" SAILWRIGHT_E2E_INSECURE="${RT_ALLOW_INSECURE_CREDENTIALS:-}" \
		${APPDATA_ENV[@]+"${APPDATA_ENV[@]}"} PATH="$RT_BIN_DIR:$PATH" \
		go -C "$RT_REPO_ROOT" test -tags e2e ./test/e2e -run '^TestVMOwnership$' -count=1 -timeout 55m -v
	rt_e2e GW-10 "E2E build-cancel through the gateway (job_cancel leaves no complete artifact)" 'TestScenarios/build-cancel$' \
		SAILWRIGHT_E2E_EXECUTOR=gateway "SAILWRIGHT_E2E_TARGET=${RT_CANCEL_TARGET:-ubuntu/desktop/$HOST_ARCH}"
	gateway_stop
	rt_check GW-11 "audit records gateway_stopped" --match '"gateway_stopped"' -- cat "$GW_STATE/audit.jsonl"
	rt_check GW-12 "gateway logs and audit contain no guest password" --nomatch '"password":"[^*]' --nomatch 'P@ssw0rd!' -- \
		sh -c "cat '$GW_STATE/audit.jsonl' '$GW_STATE'/logs/*.log 2>/dev/null"

	log="$RT_PHASE_DIR/logs/GW-serve-noapprovals.log"
	if gateway_start --no-approvals "$log"; then
		rt_check GW-20 "banner shows the safe set only" --match 'disabled \(safe set only\)' -- cat "$log"
		rt_check GW-21 "CLI creates a VM outside the gateway" --timeout 3600 -- "${SELF[@]}" create "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"}
		rt_check GW-21b "CLI starts it" --timeout 3600 -- "${SELF[@]}" start "${TARGET_ARGS[@]}" ${INSECURE[@]+"${INSECURE[@]}"}
		rt_check GW-22 "tunnel to a VM the gateway did not create is refused" --exit-nonzero --timeout 300 --match 'not created by this gateway|403' -- \
			sh -c "'$GW' vm tunnel '$RT_TARGET' 22 </dev/null"
		# The executor asks the gateway whether host changes are possible; without an approval
		# console it reports no and the scenario skips, which is the refusal being observed.
		rt_check GW-23 "provision_local is refused without an approval console (scenario skips with the gateway reason)" --timeout 600 --match 'no approval console' --match 'SKIP: TestScenarios/local-check' -- \
			env SAILWRIGHT_E2E_EXECUTOR=gateway SAILWRIGHT_E2E_OUT="$RT_PHASE_DIR/e2e-GW-23" go -C "$RT_REPO_ROOT" test -tags e2e ./test/e2e -run 'TestScenarios/local-check$' -count=1 -v
		rt_check GW-24 "cleanup CLI VM" --timeout 1800 -- "${SELF[@]}" destroy "${TARGET_ARGS[@]}"
		gateway_stop
	else
		rt_fail GW-20 "gateway starts with --no-approvals" "see $log"
	fi
	rt_manual GW-30 "approval console: CLI-created VM via MCP vm_info" \
		"Start '$GW --state-dir <new empty dir outside the checkout> serve --binary $BIN' in a terminal (no bypass), so no earlier ownership records or grants apply. Create the VM with the CLI. Ask the agent to call vm_info for $RT_TARGET: the console shows vm_access_decision with VM_NOT_CREATED_BY_GATEWAY; type approve <id>; tunnels then work. Destroy/recreate with the CLI (once with a list_targets call in between, once without): the tunnel answers 'VM was recreated outside this gateway' until vm_info is approved again."
	gateway_finish
	trap - EXIT
	rt_summary
}

# ---------------------------------------------------------------- cleanup
phase_cleanup() {
	need_bin
	rt_init cleanup
	local mode
	for mode in default isolated; do
		rt_check "CL-01-$mode" "no managed VM left ($mode app data)" \
			--json 'all(t["fields"].get("state") == "missing" for t in d["targets"])' -- \
			sh -c "if [ $mode = isolated ] && [ '$RT_APPDATA' != default ]; then export DEV_ALCHEMY_APP_DATA_DIR='$RT_APPDATA'; fi; '$BIN' destroy list --json 2>/dev/null"
	done
	local left
	left="$(processes_matching "$RT_RUN_DIR")"
	if [ -z "$left" ]; then rt_pass CL-02 "no kit processes left"; else rt_fail CL-02 "no kit processes left" "$left"; fi
	rt_check CL-03 "run directory size (delete appdata after reporting)" --exit-any -- du -sh "$RT_RUN_DIR"
	rt_summary
}

# ---------------------------------------------------------------- report
phase_report() {
	local report="$RT_RUN_DIR/report.md" dir
	{
		printf '# Release test report: %s on %s\n\n' "$RT_PLAN" "$(rt__host)"
		printf -- '- commit: %s\n- binary version: %s\n- target: %s\n- app data: %s\n\n' \
			"$(git -C "$RT_REPO_ROOT" rev-parse HEAD)" "$RT_VERSION" "$RT_TARGET" "$RT_APPDATA"
		for dir in "$RT_RUN_DIR"/*/; do
			if [ -f "$dir/summary.md" ]; then
				sed 's/^# /## /' "$dir/summary.md"
				printf '\n'
			fi
		done
	} >"$report"
	echo "report: $report"
	grep -h -E $'\tFAIL\t' "$RT_RUN_DIR"/*/results.tsv 2>/dev/null | cut -f1,4,5 || true
}

# ---------------------------------------------------------------- dispatch
if [ "${1:-}" = __sw ]; then
	shift
	sw "$@"
	exit $?
fi
if [ $# -eq 0 ]; then
	sed -n '2,9p' "$0"
	echo "phases: preflight kit cli selfupdate install provision-local provision-interactive netclass interrupt build vm gateway cleanup report"
	exit 2
fi
status=0
for phase in "$@"; do
	case "$phase" in
	preflight | kit | cli | selfupdate | install | netclass | interrupt | build | vm | gateway | cleanup | report) "phase_$phase" || status=1 ;;
	provision-local) phase_provision_local || status=1 ;;
	provision-interactive) phase_provision_interactive || status=1 ;;
	*) echo "unknown phase: $phase" >&2; exit 2 ;;
	esac
done
exit "$status"
