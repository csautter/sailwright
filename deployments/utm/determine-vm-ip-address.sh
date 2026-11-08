#!/usr/bin/env bash
set -e
# This script determines the IP address of the Windows 11 ARM64 UTM VM on macOS.

# Manual argument parsing for portability
arch="arm64"
os="windows11"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch)
		if [[ -n "$2" && ("$2" == "amd64" || "$2" == "arm64") ]]; then
			arch="$2"
			shift 2
		else
			echo "Invalid value for --arch: $2. Allowed values are 'amd64' or 'arm64'." >&2
			exit 1
		fi
		;;
	--os)
		if [[ -n "$2" && ("$2" == "windows11") ]]; then
			os="$2"
			shift 2
		elif [[ -n "$2" && ("$2" == "ubuntu-desktop" || "$2" == "ubuntu-server") ]]; then
			os="$2"
			shift 2
		else
			echo "Invalid value for --os: $2. Allowed values are 'windows11', 'ubuntu-desktop', or 'ubuntu-server'." >&2
			exit 1
		fi
		;;
	*)
		echo "Unknown option: $1" >&2
		exit 1
		;;
	esac
done

utm_vm_dir="${SAILWRIGHT_UTM_VM_DIR:-$HOME/Library/Containers/com.utmapp.UTM/Data/Documents}"
plist="$utm_vm_dir/$os-$arch-dev-alchemy.utm/config.plist"

# Read the MAC address from the plist file
mac=$(grep -A1 'MacAddress' "$plist" | grep string | awk -F'[<>]' '{print $3}')

echo "Looking for IP address associated with MAC address: $mac"

# Find the IP address associated with the MAC address
ip=$(arp -a | grep -i "$mac" | grep ifscope | awk '{print $2}' | tr -d '()')

if [ -z "$ip" ]; then
	echo "Could not find the IP address for MAC address $mac. Is the VM running?"
	exit 1
else
	echo "The IP address of the $os $arch UTM VM is: $ip"
fi
