#!/usr/bin/env bash
# Preflight check for KVM-accelerated VM self-tests inside devcontainers and
# Orca workspaces.
#
# Checks host/container CPU architecture, /dev/kvm presence and read/write
# permissions, DEV_ALCHEMY_LIBVIRT_URI, and required QEMU/libvirt/TPM/ISO
# tooling. When /dev/kvm is unavailable, prints actionable host-matrix
# guidance (including the macOS gateway path for hosts without nested KVM).
#
# Exit codes:
#   0  Ready: /dev/kvm is readable and writable and all required VM tools exist.
#   1  Invalid usage or unsupported host CPU architecture.
#   2  /dev/kvm is missing (prints host-matrix and gateway guidance).
#   3  /dev/kvm exists but lacks read/write access (prints rootless Docker ACL/group guidance).
#   4  /dev/kvm is usable, but one or more required VM tools are missing.
#
# Test/override environment variables:
#   KVM_PREFLIGHT_DEVICE           Override device path (default: /dev/kvm).
#   KVM_PREFLIGHT_ARCH             Override detected architecture (default: uname -m).
#   KVM_PREFLIGHT_PATH             Override PATH used to look up VM tools.
#   KVM_PREFLIGHT_ACCESS_OVERRIDE  Test hook only: "ok" or "denied" to bypass
#                                  kernel open() checks (e.g. when tests run as root).
#
# Written for bash 3.2+ and Linux bash.
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: kvm-preflight.sh [-h|--help]

Verify that this container can run KVM-accelerated VM self-tests with
Packer/QEMU and libvirt user-session domains (DEV_ALCHEMY_LIBVIRT_URI=qemu:///session).

Exit codes:
  0  Ready (/dev/kvm r/w and all required VM tools present)
  1  Invalid usage or unsupported CPU architecture
  2  /dev/kvm missing (prints host-matrix and macOS gateway guidance)
  3  /dev/kvm exists without read/write access (rootless Docker ACL/group hint)
  4  Required VM tooling missing
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "kvm-preflight: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

KVM_DEVICE="${KVM_PREFLIGHT_DEVICE:-/dev/kvm}"
RAW_ARCH="${KVM_PREFLIGHT_ARCH:-$(uname -m 2>/dev/null || echo unknown)}"
TOOL_PATH="${KVM_PREFLIGHT_PATH-$PATH}"
ACCESS_OVERRIDE="${KVM_PREFLIGHT_ACCESS_OVERRIDE:-}"

case "$RAW_ARCH" in
    x86_64|amd64)
        NORM_ARCH="amd64"
        QEMU_BIN="qemu-system-x86_64"
        ;;
    aarch64|arm64)
        NORM_ARCH="arm64"
        QEMU_BIN="qemu-system-aarch64"
        ;;
    *)
        echo "kvm-preflight: unsupported CPU architecture '${RAW_ARCH}' (supported: amd64/x86_64, arm64/aarch64)" >&2
        exit 1
        ;;
esac

echo "kvm-preflight: arch=${NORM_ARCH} (raw=${RAW_ARCH}), device=${KVM_DEVICE}"
if [ -n "${DEV_ALCHEMY_LIBVIRT_URI:-}" ]; then
    echo "kvm-preflight: DEV_ALCHEMY_LIBVIRT_URI=${DEV_ALCHEMY_LIBVIRT_URI}"
    if [ "$DEV_ALCHEMY_LIBVIRT_URI" != "qemu:///session" ]; then
        echo "kvm-preflight: note: container self-tests expect DEV_ALCHEMY_LIBVIRT_URI=qemu:///session for unprivileged libvirt session mode." >&2
    fi
else
    echo "kvm-preflight: warning: DEV_ALCHEMY_LIBVIRT_URI is not set (recommended: export DEV_ALCHEMY_LIBVIRT_URI=qemu:///session; Sailwright defaults to qemu:///system)." >&2
fi

print_no_kvm_guidance() {
    cat >&2 <<EOF
kvm-preflight: ${KVM_DEVICE} not found; hardware-accelerated KVM self-tests cannot run in this container.

Choose the path that matches your host:
  1. Ubuntu (AMD, rootless Docker -> amd64 guests):
     Verify /dev/kvm exists on the host (enable SVM/VT-x in BIOS and load kvm_amd/kvm_intel),
     then recreate this container so .devcontainer/initialize-host.sh and scripts/orca-vm pass
     /dev/kvm through automatically.
  2. Apple Silicon M3+/M5 (Colima -> arm64 guests):
     Start Colima with nested virtualization enabled (requires Apple M3 or newer and macOS 15+):
       colima start --nested-virtualization
     Then recreate this container so /dev/kvm is passed through from the Colima VM.
  3. Apple Silicon M2 (Colima -> no nested virtualization before M3):
     Hardware nested virtualization is not available on M1/M2 Macs, and software emulation (TCG)
     inside the container is not suitable for full VM self-tests (especially Windows).
     Use the gateway path instead: run VMs natively on macOS via sailwright-gateway (Tart/UTM)
     while agents remain inside the container (see docs/agent-gateway.md).
EOF
}

if [ ! -e "$KVM_DEVICE" ]; then
    print_no_kvm_guidance
    exit 2
fi

kvm_has_rw() {
    case "$ACCESS_OVERRIDE" in
        ok) return 0 ;;
        denied) return 1 ;;
        '') ;;
        *)
            echo "kvm-preflight: invalid KVM_PREFLIGHT_ACCESS_OVERRIDE value '${ACCESS_OVERRIDE}' (expected 'ok' or 'denied')" >&2
            exit 1
            ;;
    esac
    [ -r "$KVM_DEVICE" ] && [ -w "$KVM_DEVICE" ] && (exec 3<>"$KVM_DEVICE") 2>/dev/null
}

if ! kvm_has_rw; then
    cat >&2 <<EOF
kvm-preflight: ${KVM_DEVICE} exists, but the current user lacks read/write access.

Under rootless Docker, container root maps to your unprivileged host user and
supplementary host groups such as 'kvm' are typically not inherited in the user
namespace.
  - Verify host passthrough:
      docker run --rm --device /dev/kvm debian:trixie sh -c 'test -r /dev/kvm && test -w /dev/kvm && echo ok'
  - Check host permissions and ACLs:
      ls -l /dev/kvm && getfacl /dev/kvm
  - If systemd-logind uaccess ACLs only apply to local desktop sessions, grant
    persistent user access on the host via a udev rule or ACL (e.g. setfacl -m u:\$USER:rw /dev/kvm).
EOF
    exit 3
fi

required_tools=(
    "$QEMU_BIN"
    qemu-img
    virsh
    virt-install
    swtpm
    xorriso
)

missing_tools=()
for tool in "${required_tools[@]}"; do
    if ! PATH="$TOOL_PATH" command -v "$tool" >/dev/null 2>&1; then
        missing_tools+=("$tool")
    fi
done

if [ "${#missing_tools[@]}" -gt 0 ]; then
    echo "kvm-preflight: missing required VM tooling for ${NORM_ARCH}: ${missing_tools[*]}" >&2
    echo "kvm-preflight: rebuild the devcontainer image (.devcontainer/Dockerfile) to install the VM packages." >&2
    exit 4
fi

echo "kvm-preflight: OK (${KVM_DEVICE} is r/w and all required ${NORM_ARCH} VM tools are available)."
exit 0
