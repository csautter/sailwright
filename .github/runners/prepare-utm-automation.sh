#!/bin/bash
# runner-guest-script: prepare-utm-automation
# Prepare UTM inside a Tart runner VM for unattended AppleScript control.
#
# Runs inside the guest as the SSH user, e.g.:
#   ssh admin@<vm> /bin/bash -s -- [--verify] < prepare-utm-automation.sh
#
# GitHub Actions jobs run below the runner's SSH session, so macOS attributes
# their Apple Events to /usr/libexec/sshd-keygen-wrapper. Without preparation
# the first `osascript -e 'tell application "UTM" ...'` waits on GUI prompts
# that nobody can answer:
#   1. Gatekeeper's first-launch confirmation for the quarantined Homebrew cask.
#      LaunchServices keeps UTM suspended until someone clicks "Open".
#   2. The Automation consent (kTCCServiceAppleEvents) for SSH -> UTM.
# This script removes the quarantine attribute and stores the Automation grant
# in the user's TCC database. Writing TCC.db needs SIP disabled in the guest,
# which is the case for the Cirrus Labs macOS base images.
#
# --verify launches UTM once through Apple Events (bounded by a timeout) and
# quits it again, so a golden image build fails early instead of every CI job.
#
# Optional environment variables (mainly for tests):
#   UTM_APP                    - UTM bundle path        (default: /Applications/UTM.app)
#   TCC_DB                     - user TCC database      (default: ~/Library/Application Support/com.apple.TCC/TCC.db)
#   UTM_AUTOMATION_CLIENT      - Apple Events sender    (default: /usr/libexec/sshd-keygen-wrapper)
#   UTM_AUTOMATION_CLIENT_REQ  - its code requirement   (default: Apple-signed sshd-keygen-wrapper)
#   UTM_VERIFY_TIMEOUT         - seconds for --verify   (default: 60)
set -euo pipefail

UTM_APP="${UTM_APP:-/Applications/UTM.app}"
TCC_DB="${TCC_DB:-${HOME}/Library/Application Support/com.apple.TCC/TCC.db}"
UTM_AUTOMATION_CLIENT="${UTM_AUTOMATION_CLIENT:-/usr/libexec/sshd-keygen-wrapper}"
UTM_AUTOMATION_CLIENT_REQ="${UTM_AUTOMATION_CLIENT_REQ:-identifier \"com.apple.sshd-keygen-wrapper\" and anchor apple}"
UTM_VERIFY_TIMEOUT="${UTM_VERIFY_TIMEOUT:-60}"

verify=false
for arg in "$@"; do
	case "$arg" in
		--verify) verify=true ;;
		*)
			echo "ERROR: unknown argument: ${arg}" >&2
			exit 2
			;;
	esac
done

if [[ ! -d "${UTM_APP}" ]]; then
	if [[ "${verify}" == "true" ]]; then
		echo "ERROR: ${UTM_APP} not found." >&2
		exit 1
	fi
	echo "UTM is not installed at ${UTM_APP}; nothing to prepare."
	exit 0
fi

bundle_id="$(defaults read "${UTM_APP}/Contents/Info.plist" CFBundleIdentifier)"
# Both values end up in SQL string literals below.
if [[ ! "${bundle_id}" =~ ^[A-Za-z0-9.-]+$ ]]; then
	echo "ERROR: unexpected UTM bundle identifier: ${bundle_id}" >&2
	exit 1
fi
if [[ ! "${UTM_AUTOMATION_CLIENT}" =~ ^[A-Za-z0-9._/-]+$ ]]; then
	echo "ERROR: unexpected Apple Events client: ${UTM_AUTOMATION_CLIENT}" >&2
	exit 1
fi

# ── 1. Gatekeeper quarantine ─────────────────────────────────────────────────
sudo xattr -dr com.apple.quarantine "${UTM_APP}"
if xattr -p com.apple.quarantine "${UTM_APP}" >/dev/null 2>&1; then
	echo "ERROR: ${UTM_APP} is still quarantined." >&2
	exit 1
fi
echo "UTM: Gatekeeper quarantine cleared."

# ── 2. Automation (Apple Events) grant ───────────────────────────────────────
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

hex_file() {
	od -An -tx1 -v "$1" | tr -d ' \n'
}

printf '%s\n' "${UTM_AUTOMATION_CLIENT_REQ}" | csreq -r- -b "${work_dir}/client.csreq"
target_req="$(codesign -d -r- "${UTM_APP}" 2>/dev/null | sed -n 's/^designated => //p')"
if [[ -z "${target_req}" ]]; then
	echo "ERROR: could not read the designated requirement of ${UTM_APP}." >&2
	exit 1
fi
printf '%s\n' "${target_req}" | csreq -r- -b "${work_dir}/target.csreq"
client_hex="$(hex_file "${work_dir}/client.csreq")"
target_hex="$(hex_file "${work_dir}/target.csreq")"
if [[ ! "${client_hex}${target_hex}" =~ ^[0-9a-f]+$ ]]; then
	echo "ERROR: compiling the code requirements failed." >&2
	exit 1
fi

# auth_value 2 = allowed, auth_reason 3 = user set, client_type 1 = path,
# indirect_object_identifier_type 0 = bundle identifier.
sudo sqlite3 "${TCC_DB}" "INSERT OR REPLACE INTO access (service, client, client_type, auth_value, auth_reason, auth_version, csreq, policy_id, indirect_object_identifier_type, indirect_object_identifier, indirect_object_code_identity, flags) VALUES ('kTCCServiceAppleEvents', '${UTM_AUTOMATION_CLIENT}', 1, 2, 3, 1, X'${client_hex}', NULL, 0, '${bundle_id}', X'${target_hex}', 0);"
granted="$(sudo sqlite3 "${TCC_DB}" "SELECT auth_value FROM access WHERE service = 'kTCCServiceAppleEvents' AND client = '${UTM_AUTOMATION_CLIENT}' AND indirect_object_identifier = '${bundle_id}';")"
if [[ "${granted}" != "2" ]]; then
	echo "ERROR: Automation grant for ${UTM_AUTOMATION_CLIENT} -> ${bundle_id} was not stored in ${TCC_DB}." >&2
	exit 1
fi
echo "UTM: Automation granted for ${UTM_AUTOMATION_CLIENT} -> ${bundle_id}."

# ── 3. Optional end-to-end check ─────────────────────────────────────────────
if [[ "${verify}" == "true" ]]; then
	if ! perl -e 'alarm shift; exec @ARGV or die "exec: $!"' "${UTM_VERIFY_TIMEOUT}" \
		osascript -e "tell application id \"${bundle_id}\" to get name of every virtual machine" >/dev/null; then
		echo "ERROR: UTM did not answer Apple Events within ${UTM_VERIFY_TIMEOUT}s after preparation." >&2
		pgrep -lx UTM >&2 || true
		exit 1
	fi
	osascript -e "tell application id \"${bundle_id}\" to quit" >/dev/null 2>&1 || true
	echo "UTM: Apple Events answered."
fi
