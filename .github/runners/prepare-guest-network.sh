#!/bin/bash
# runner-guest-script: prepare-guest-network
#
# Configure DNS inside a Tart runner VM and wait until the guest can reach
# GitHub before the runner registers.
#
# Runs inside the guest as the SSH user, e.g.:
#   ssh admin@<vm> /bin/bash -s -- --dns "192.168.1.1" --url https://github.com/ < prepare-guest-network.sh
#
# With Tart's NAT (vmnet shared) networking the guest gets 192.168.64.1 as its
# DHCP DNS server. On some hosts that forwarder never answers, so the runner
# script passes an explicit resolver (by default the host's LAN router). The
# readiness check proves system DNS and HTTPS with certificate verification
# from inside the guest; an IP address alone does not mean the VM can work.
#
# Arguments:
#   --dns LIST      space-separated resolver IPs, "dhcp" to clear manual servers,
#                   or "keep" to leave the guest's DNS settings untouched (default)
#   --service NAME  macOS network service to configure (default: Ethernet)
#   --url URL       HTTPS endpoint the guest must reach (repeatable)
#   --timeout SEC   total time for the readiness check (default: 90; 0 = one attempt)
#   --interval SEC  pause between attempts (default: 3)
set -euo pipefail

dns="keep"
service="Ethernet"
timeout=90
interval=3
urls=()

while [[ $# -gt 0 ]]; do
	case "$1" in
		--dns) dns="${2:?--dns needs a value}"; shift 2 ;;
		--service) service="${2:?--service needs a value}"; shift 2 ;;
		--url) urls+=("${2:?--url needs a value}"); shift 2 ;;
		--timeout) timeout="${2:?--timeout needs a value}"; shift 2 ;;
		--interval) interval="${2:?--interval needs a value}"; shift 2 ;;
		*)
			echo "ERROR: unknown argument: $1" >&2
			exit 2
			;;
	esac
done

if [[ ! "${timeout}" =~ ^[0-9]+$ || ! "${interval}" =~ ^[0-9]+$ ]]; then
	echo "ERROR: --timeout and --interval must be whole seconds." >&2
	exit 2
fi

# ── DNS ──────────────────────────────────────────────────────────────────────
case "${dns}" in
	keep) ;;
	dhcp)
		sudo networksetup -setdnsservers "${service}" Empty
		echo "Guest DNS: using DHCP servers on '${service}'."
		;;
	*)
		read -r -a servers <<<"${dns}"
		if [[ ${#servers[@]} -eq 0 ]]; then
			echo "ERROR: --dns needs at least one server." >&2
			exit 2
		fi
		for server in "${servers[@]}"; do
			if [[ ! "${server}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ && ! "${server}" =~ ^[0-9A-Fa-f:]+$ ]]; then
				echo "ERROR: not an IP address: ${server}" >&2
				exit 2
			fi
		done
		sudo networksetup -setdnsservers "${service}" "${servers[@]}"
		echo "Guest DNS: ${servers[*]} on '${service}'."
		;;
esac
if [[ "${dns}" != "keep" ]]; then
	sudo dscacheutil -flushcache || true
	sudo killall -HUP mDNSResponder 2>/dev/null || true
fi

# ── Readiness check ──────────────────────────────────────────────────────────
if [[ ${#urls[@]} -eq 0 ]]; then
	exit 0
fi

url_host() {
	local rest="${1#*://}"
	rest="${rest%%/*}"
	printf '%s\n' "${rest%%:*}"
}

resolvers() {
	scutil --dns 2>/dev/null | awk '/nameserver\[[0-9]+\]/ {print $3}' | awk '!seen[$0]++' | paste -sd, - || true
}

deadline=$((SECONDS + timeout))
attempt=0
while true; do
	attempt=$((attempt + 1))
	failure=""
	for url in "${urls[@]}"; do
		host="$(url_host "${url}")"
		if ! dscacheutil -q host -a name "${host}" | grep -q 'address:'; then
			failure="system DNS cannot resolve ${host}"
			break
		fi
		# Any HTTP status proves DNS, routing and a verified TLS handshake.
		code="$(curl -sS -o /dev/null --max-time 10 -w '%{http_code}' "${url}" 2>/dev/null || true)"
		if [[ -z "${code}" || "${code}" == "000" ]]; then
			failure="HTTPS to ${url} failed"
			break
		fi
	done
	if [[ -z "${failure}" ]]; then
		echo "Guest network ready after ${attempt} attempt(s) (resolvers: $(resolvers))."
		exit 0
	fi
	if ((SECONDS >= deadline)); then
		echo "ERROR: guest network not ready after ${timeout}s (${attempt} attempt(s)): ${failure}; resolvers: $(resolvers)." >&2
		exit 1
	fi
	sleep "${interval}"
done
