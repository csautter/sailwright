#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/prepare-tart-base-tests.XXXXXX")"
trap 'rm -rf "${TEST_ROOT}"' EXIT

fail() {
	printf 'not ok - %s\n' "$*" >&2
	exit 1
}

script="${SCRIPT_DIR}/prepare-tart-base.sh"
bin="${TEST_ROOT}/bin"
version_file="${TEST_ROOT}/azure-cli-version"
log="${TEST_ROOT}/brew.log"
mkdir -p "$bin"

cat >"${bin}/brew" <<'EOF_BREW'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_BREW_LOG}"
case "$*" in
	'list --cask --versions azure-cli')
		[[ -s "${FAKE_BREW_VERSION_FILE}" ]] || exit 1
		printf 'azure-cli %s\n' "$(<"${FAKE_BREW_VERSION_FILE}")"
		;;
	'install --cask azure-cli'|'upgrade --cask azure-cli')
		printf '999.0.0\n' >"${FAKE_BREW_VERSION_FILE}"
		;;
	*) exit 1 ;;
esac
EOF_BREW
printf '#!/usr/bin/env bash\nexit 0\n' >"${bin}/az"
chmod +x "${bin}/brew" "${bin}/az"

# Load only the pin, the helper definitions, and the Azure CLI call.
# Replace the SSH step with a local execution of its generated body.
cat >"${TEST_ROOT}/provision.sh" <<'EOF_PROVISION'
set -e
provision_step() {
	bash -ec "$2"
}
EOF_PROVISION
awk '
	/^AZURE_CLI_VERSION=/ { print }
	/^brew_version_compare\(\) \{/ { helpers = 1 }
	helpers { print }
	helpers && /^brew_install\(\) \{/ { install = 1 }
	install && /^}$/ { helpers = 0; install = 0 }
	/^brew_install "Azure CLI"/ { print }
' "$script" >>"${TEST_ROOT}/provision.sh"

check_provisioning() {
	local scenario="$1" initial_version="$2"
	shift 2

	printf '%s' "$initial_version" >"$version_file"
	: >"$log"
	PATH="${bin}:${PATH}" FAKE_BREW_LOG="$log" FAKE_BREW_VERSION_FILE="$version_file" \
		bash "${TEST_ROOT}/provision.sh" >"${TEST_ROOT}/output" 2>&1 ||
		fail "Azure CLI cask provisioning (${scenario}) failed: $(cat "${TEST_ROOT}/output" "$log")"
	diff -u <(printf '%s\n' "$@") "$log" || fail "unexpected Homebrew calls (${scenario})"
	printf 'ok - Azure CLI cask provisioning (%s)\n' "$scenario"
}

list='list --cask --versions azure-cli'
check_provisioning missing "" "$list" 'install --cask azure-cli' "$list"
check_provisioning current '999.0.0' "$list"
check_provisioning older '0.0.1' "$list" 'upgrade --cask azure-cli' "$list"
