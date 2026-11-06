#!/usr/bin/env bash
# Provision libvirt only on a disposable GitHub-hosted runner, then exercise a
# prebuilt guest through the real gateway. Never run this on a shared VM host.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted ]] || {
  echo 'Requires a disposable GitHub-hosted Linux runner' >&2; exit 1;
}
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]]
: "${RUNNER_TEMP:?}" "${GITHUB_WORKSPACE:?}" "${GITHUB_REPOSITORY_OWNER:?}"
artifact="$GITHUB_WORKSPACE/artifact/gateway-guest-access"
mkdir -p "$artifact"
root=$(mktemp -d "$RUNNER_TEMP/gateway-access.XXXXXX")
# Keep paths below /var/tmp: QEMU needs directory traversal outside runner HOME.
export DEV_ALCHEMY_LIBVIRT_IMAGE_DIR
DEV_ALCHEMY_LIBVIRT_IMAGE_DIR=$(mktemp -d /var/tmp/gateway-access-images.XXXXXX)
chmod 755 "$DEV_ALCHEMY_LIBVIRT_IMAGE_DIR"
export DEV_ALCHEMY_LIBVIRT_URI=qemu:///system
export DEV_ALCHEMY_APP_DATA_DIR="$root/data/app"
export DEV_ALCHEMY_CACHE_DIR="$DEV_ALCHEMY_APP_DATA_DIR/cache"
unset SAILWRIGHT_GATEWAY_URL SAILWRIGHT_GATEWAY_TOKEN
export SAILWRIGHT_GATEWAY_DIR="$root/agent"
export SAILWRIGHT_E2E_TARGET=ubuntu/server/amd64
export SAILWRIGHT_VM_ACCESS_E2E=1
export LIBVIRT_UBUNTU_ANSIBLE_USER=packer
export LIBVIRT_UBUNTU_ANSIBLE_PASSWORD='P@ssw0rd!'
export PATH="$root/bin:$PATH"
domain=ubuntu-server-amd64-dev-alchemy
gateway_pid=''
preflight=false
cleanup() {
  result=$?
  trap - EXIT
  set +e
  if [[ -n $gateway_pid ]]; then
    kill -TERM "$gateway_pid"
    for ((attempt=0; attempt<30; attempt++)); do
      kill -0 "$gateway_pid" 2>/dev/null || break
      sleep 1
    done
    if kill -0 "$gateway_pid" 2>/dev/null; then
      kill -KILL "$gateway_pid"
      echo 'Gateway required forced shutdown' >>"$artifact/cleanup.txt"
      result=1
    fi
    wait "$gateway_pid"
  fi
  # Fallback after a failed/timed-out test. No sweeping virsh destroy commands:
  # only our target, absent at preflight, with its disk in this run's directory.
  if [[ $preflight == true ]]; then
    if sudo virsh -c "$DEV_ALCHEMY_LIBVIRT_URI" dominfo "$domain" >/dev/null 2>&1; then
      if sudo virsh -c "$DEV_ALCHEMY_LIBVIRT_URI" domblklist "$domain" --details |
          awk -v disk="$DEV_ALCHEMY_LIBVIRT_IMAGE_DIR/$domain.qcow2" '$3 == "vda" && $4 == disk { found=1 } END { exit !found }'; then
        timeout 5m sailwright destroy ubuntu --type server --arch amd64 >"$artifact/fallback-cleanup.log" 2>&1 || result=1
      else
        echo 'Refusing fallback cleanup: domain disk does not match this run' >>"$artifact/cleanup.txt"
        result=1
      fi
    else
      # A failed create may have cloned its disk before defining the domain.
      timeout 5m sailwright destroy ubuntu --type server --arch amd64 >"$artifact/fallback-cleanup.log" 2>&1 || result=1
    fi
    if ! virsh -c "$DEV_ALCHEMY_LIBVIRT_URI" list --all --name >"$artifact/domains-after.txt"; then
      echo 'FAIL: could not inventory remaining domains' >>"$artifact/cleanup.txt"; result=1
    elif [[ $(<"$artifact/domains-after.txt") == *[![:space:]]* ]]; then
      echo 'FAIL: domains remain' >>"$artifact/cleanup.txt"; result=1
    elif [[ -n $(find "$DEV_ALCHEMY_LIBVIRT_IMAGE_DIR" -type f -print -quit) ]]; then
      echo 'FAIL: managed disks remain' >>"$artifact/cleanup.txt"; result=1
    else
      echo 'PASS: no domains or managed disks remain' >>"$artifact/cleanup.txt"
    fi
  fi
  if [[ -f "$root/state/audit.jsonl" ]]; then
    cp "$root/state/audit.jsonl" "$artifact/audit.jsonl"
    python3 scripts/ci/verify-gateway-guest-audit.py "$artifact/audit.jsonl" >"$artifact/audit-check.txt" 2>&1 || result=1
  else
    echo 'FAIL: no gateway audit produced' >"$artifact/audit-check.txt"; result=1
  fi
  # Deliberately exclude token, gateway.yaml, inventories, VM info, ownership
  # state and private gateway logs from uploaded artifacts.
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
sudo apt-get update
sudo apt-get install -y libvirt-daemon-system libvirt-clients qemu-system-x86 qemu-utils virtinst sshpass acl
sudo systemctl start libvirtd
# ACLs affect only this throwaway runner; no group/session refresh required.
sudo setfacl -m "u:$(id -un):rw" /var/run/libvirt/libvirt-sock /dev/kvm
virsh -c "$DEV_ALCHEMY_LIBVIRT_URI" list --all --name >"$artifact/domains-before.txt"
# Bash checks the inventory itself: missing optional tools cannot bypass safety.
if [[ $(<"$artifact/domains-before.txt") == *[![:space:]]* ]]; then
  echo 'Refusing to use a runner with existing VMs' >&2; exit 1
fi
preflight=true
if ! sudo virsh -c "$DEV_ALCHEMY_LIBVIRT_URI" net-info default | awk '/Active:/ {exit ($2 != "yes")}'; then
  sudo virsh -c "$DEV_ALCHEMY_LIBVIRT_URI" net-start default
fi
mkdir -p "$root/bin" "$root/state"
go build -o "$root/bin/sailwright" ./cmd/main.go
go build -o "$root/bin/sailwright-gateway" ./cmd/sailwright-gateway
{
  git rev-parse HEAD
  uname -srmo
  go version
  virsh --version
  qemu-system-x86_64 --version
} >"$artifact/versions.txt"
# Pull the published Linux build; image production is a separate workflow.
# Registry/download failures fail this job, rather than silently skipping access.
sailwright pull "ghcr.io/${GITHUB_REPOSITORY_OWNER,,}/ubuntu-24:server-amd64-linux-build" \
  --os ubuntu --type server --arch amd64 --host-os linux --no-docker-credentials
cat >"$root/state/gateway.yaml" <<CONFIG
state_isolation: isolated
data_dir: $root/data
agent_dir: $root/agent
listen: off
min_free_disk_gb: -1
clean_prompt_min_gb: -1
env_passthrough:
  - DEV_ALCHEMY_LIBVIRT_URI
  - DEV_ALCHEMY_LIBVIRT_IMAGE_DIR
  - LIBVIRT_UBUNTU_ANSIBLE_USER
  - LIBVIRT_UBUNTU_ANSIBLE_PASSWORD
CONFIG
sailwright-gateway --state-dir "$root/state" serve --binary "$root/bin/sailwright" --bypass-approvals >"$root/gateway.log" 2>&1 &
gateway_pid=$!
for ((attempt=0; attempt<60; attempt++)); do
  [[ -S "$SAILWRIGHT_GATEWAY_DIR/gateway.sock" ]] && break
  kill -0 "$gateway_pid" || { echo 'Gateway failed to start' >&2; exit 1; }
  sleep 1
done
[[ -S "$SAILWRIGHT_GATEWAY_DIR/gateway.sock" ]]
# pipefail preserves the test exit status; timeout still leaves cleanup budget.
timeout --signal=TERM --kill-after=1m 58m \
  go test -tags e2e ./test/e2e -run '^TestVMAccess$' -count=1 -timeout 56m -v 2>&1 | tee "$artifact/guest-access.log"
