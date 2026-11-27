#!/usr/bin/env bash
# Tests for scripts/orca-vm/kvm-preflight.sh covering all documented exit codes,
# architecture selection, rootless Docker permission hints, and host-matrix
# guidance (Ubuntu AMD, M3+/M5 Colima, M2 Colima -> gateway path).
set -uo pipefail

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFLIGHT="${TESTS_DIR}/../kvm-preflight.sh"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/kvm-preflight-tests.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

failures=0
passes=0
pass() { passes=$((passes + 1)); }
fail() { failures=$((failures + 1)); echo "FAIL: $*" >&2; }
assert_eq() { # name expected actual
    if [ "$2" = "$3" ]; then pass; else fail "$1"$'\n'"  expected: $2"$'\n'"  actual:   $3"; fi
}
assert_true() { # name command...
    local name="$1"; shift
    if "$@"; then pass; else fail "$name"; fi
}
assert_false() { # name command...
    local name="$1"; shift
    if "$@"; then fail "$name"; else pass; fi
}

FAKE_KVM="${TMP}/fake-kvm"
: >"$FAKE_KVM"
MISSING_KVM="${TMP}/missing-kvm"

# Build synthetic tool directories for amd64, arm64, and partial toolsets.
BIN_AMD64="${TMP}/bin-amd64"
BIN_ARM64="${TMP}/bin-arm64"
BIN_PARTIAL="${TMP}/bin-partial"
mkdir -p "$BIN_AMD64" "$BIN_ARM64" "$BIN_PARTIAL"

make_stub() {
    printf '#!/bin/sh\nexit 0\n' >"$1"
    chmod +x "$1"
}

for t in qemu-img virsh virt-install passt swtpm xorriso bsdtar sshpass qemu-system-x86_64; do
    make_stub "${BIN_AMD64}/${t}"
done
for t in qemu-img virsh virt-install passt swtpm xorriso bsdtar sshpass qemu-system-aarch64; do
    make_stub "${BIN_ARM64}/${t}"
done
for t in qemu-img virsh virt-install xorriso; do
    make_stub "${BIN_PARTIAL}/${t}"
done

# --- --help and invalid usage ------------------------------------------------
help_out="$(bash "$PREFLIGHT" --help 2>&1)"
help_rc=$?
assert_eq "preflight --help exits 0" "0" "$help_rc"
assert_true "preflight --help documents exit 2" grep -qE '^  2  ' <<<"$help_out"
assert_true "preflight --help documents exit 3" grep -qE '^  3  ' <<<"$help_out"
assert_true "preflight --help documents exit 4" grep -qE '^  4  ' <<<"$help_out"

bad_out="$(bash "$PREFLIGHT" --unknown-flag 2>&1)"
bad_rc=$?
assert_eq "preflight unknown flag exits 1" "1" "$bad_rc"
assert_true "preflight unknown flag reports error" grep -q 'unknown argument' <<<"$bad_out"

arch_out="$(KVM_PREFLIGHT_ARCH=s390x bash "$PREFLIGHT" 2>&1)"
arch_rc=$?
assert_eq "preflight unsupported arch exits 1" "1" "$arch_rc"
assert_true "preflight unsupported arch reports error" grep -q 'unsupported CPU architecture' <<<"$arch_out"

# --- Exit 2: /dev/kvm missing (host matrix guidance) -------------------------
no_kvm_arm64_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=aarch64 \
    KVM_PREFLIGHT_DEVICE="$MISSING_KVM" \
    KVM_PREFLIGHT_PATH="$BIN_ARM64" \
    bash "$PREFLIGHT" 2>&1
)"
no_kvm_arm64_rc=$?
assert_eq "preflight missing /dev/kvm (arm64) exits 2" "2" "$no_kvm_arm64_rc"
assert_true "preflight missing /dev/kvm mentions Ubuntu AMD host" \
    grep -q 'Ubuntu (AMD, rootless Docker' <<<"$no_kvm_arm64_out"
assert_true "preflight missing /dev/kvm mentions colima start --nested-virtualization" \
    grep -qF 'colima start --nested-virtualization' <<<"$no_kvm_arm64_out"
assert_true "preflight missing /dev/kvm mentions Apple Silicon M2 Colima limitation" \
    grep -q 'Apple Silicon M2' <<<"$no_kvm_arm64_out"
assert_true "preflight missing /dev/kvm points to sailwright-gateway path" \
    grep -q 'sailwright-gateway' <<<"$no_kvm_arm64_out"

no_kvm_amd64_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=x86_64 \
    KVM_PREFLIGHT_DEVICE="$MISSING_KVM" \
    KVM_PREFLIGHT_PATH="$BIN_AMD64" \
    bash "$PREFLIGHT" 2>&1
)"
no_kvm_amd64_rc=$?
assert_eq "preflight missing /dev/kvm (amd64) exits 2" "2" "$no_kvm_amd64_rc"
assert_true "preflight missing /dev/kvm (amd64) reports normalized arch" \
    grep -q 'arch=amd64' <<<"$no_kvm_amd64_out"

# --- Exit 3: /dev/kvm exists without r/w permissions -------------------------
denied_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=x86_64 \
    KVM_PREFLIGHT_DEVICE="$FAKE_KVM" \
    KVM_PREFLIGHT_ACCESS_OVERRIDE=denied \
    KVM_PREFLIGHT_PATH="$BIN_AMD64" \
    bash "$PREFLIGHT" 2>&1
)"
denied_rc=$?
assert_eq "preflight denied /dev/kvm exits 3" "3" "$denied_rc"
assert_true "preflight denied /dev/kvm explains rootless Docker" \
    grep -q 'rootless Docker' <<<"$denied_out"
assert_true "preflight denied /dev/kvm mentions ACL / kvm group" \
    grep -q 'setfacl' <<<"$denied_out"

# --- Exit 4: missing VM tooling ----------------------------------------------
missing_arm_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=aarch64 \
    KVM_PREFLIGHT_DEVICE="$FAKE_KVM" \
    KVM_PREFLIGHT_ACCESS_OVERRIDE=ok \
    KVM_PREFLIGHT_PATH="$BIN_PARTIAL" \
    bash "$PREFLIGHT" 2>&1
)"
missing_arm_rc=$?
assert_eq "preflight missing tools (arm64) exits 4" "4" "$missing_arm_rc"
assert_true "preflight missing tools (arm64) lists qemu-system-aarch64" \
    grep -q 'qemu-system-aarch64' <<<"$missing_arm_out"
assert_true "preflight missing tools (arm64) lists swtpm" \
    grep -q 'swtpm' <<<"$missing_arm_out"
assert_true "preflight missing tools (arm64) lists passt" \
    grep -q 'passt' <<<"$missing_arm_out"
assert_true "preflight missing tools (arm64) lists bsdtar" \
    grep -q 'bsdtar' <<<"$missing_arm_out"
assert_true "preflight missing tools (arm64) lists sshpass" \
    grep -q 'sshpass' <<<"$missing_arm_out"

missing_x86_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=x86_64 \
    KVM_PREFLIGHT_DEVICE="$FAKE_KVM" \
    KVM_PREFLIGHT_ACCESS_OVERRIDE=ok \
    KVM_PREFLIGHT_PATH="$BIN_ARM64" \
    bash "$PREFLIGHT" 2>&1
)"
missing_x86_rc=$?
assert_eq "preflight missing x86 QEMU on amd64 exits 4" "4" "$missing_x86_rc"
assert_true "preflight missing x86 QEMU lists qemu-system-x86_64" \
    grep -q 'qemu-system-x86_64' <<<"$missing_x86_out"

# --- Exit 0: ready (amd64 and arm64) and DEV_ALCHEMY_LIBVIRT_URI hint --------
ok_amd64_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=x86_64 \
    KVM_PREFLIGHT_DEVICE="$FAKE_KVM" \
    KVM_PREFLIGHT_ACCESS_OVERRIDE=ok \
    KVM_PREFLIGHT_PATH="$BIN_AMD64" \
    bash "$PREFLIGHT" 2>&1
)"
ok_amd64_rc=$?
assert_eq "preflight ready (amd64) exits 0" "0" "$ok_amd64_rc"
assert_true "preflight ready (amd64) prints OK" grep -q 'OK' <<<"$ok_amd64_out"
assert_false "preflight ready with session URI prints no warning" \
    grep -q 'warning:' <<<"$ok_amd64_out"

ok_arm64_out="$(
    DEV_ALCHEMY_LIBVIRT_URI=qemu:///session \
    KVM_PREFLIGHT_ARCH=aarch64 \
    KVM_PREFLIGHT_DEVICE="$FAKE_KVM" \
    KVM_PREFLIGHT_ACCESS_OVERRIDE=ok \
    KVM_PREFLIGHT_PATH="$BIN_ARM64" \
    bash "$PREFLIGHT" 2>&1
)"
ok_arm64_rc=$?
assert_eq "preflight ready (arm64) exits 0" "0" "$ok_arm64_rc"
assert_true "preflight ready (arm64) reports arm64" grep -q 'arm64 VM tools' <<<"$ok_arm64_out"

unset_uri_out="$(
    unset DEV_ALCHEMY_LIBVIRT_URI
    KVM_PREFLIGHT_ARCH=x86_64 \
    KVM_PREFLIGHT_DEVICE="$FAKE_KVM" \
    KVM_PREFLIGHT_ACCESS_OVERRIDE=ok \
    KVM_PREFLIGHT_PATH="$BIN_AMD64" \
    bash "$PREFLIGHT" 2>&1
)"
unset_uri_rc=$?
assert_eq "preflight ready with unset DEV_ALCHEMY_LIBVIRT_URI exits 0" "0" "$unset_uri_rc"
assert_true "preflight warns when DEV_ALCHEMY_LIBVIRT_URI is unset" \
    grep -q 'DEV_ALCHEMY_LIBVIRT_URI is not set' <<<"$unset_uri_out"

echo "kvm-preflight tests: ${passes} passed, ${failures} failed"
[ "$failures" -eq 0 ]
