#!/usr/bin/env bash

# Sailwright quick-start automation.
#
# Runs the full "getting started" lifecycle for one VM target as a single
# command, records the run (terminal session + VM graphical display), and
# always cleans up afterwards.
#
# Lifecycle:  install -> build -> create -> start -> provision --check
#             -> provision -> (stop -> destroy)
#
# Primary supported host is Linux + QEMU/KVM (libvirt). The VM-display
# recording only runs there; on other hosts (or when the tools/endpoint are
# unavailable) it is skipped with a warning and the workflow still runs.

set -euo pipefail

# Capture the original argv up-front so the terminal-recording layer can
# re-exec this script unchanged inside asciinema/script.
ORIG_ARGS=("$@")

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
OS="ubuntu"
TYPE="server"
ARCH="amd64"
SKIP_INSTALL="false"
SKIP_BUILD="false"
NO_CACHE="false"
KEEP="false"
RECORD="true"
RECORD_DIR="./artifact"

usage() {
	cat <<'EOF'
Usage: scripts/quickstart.sh [options]

Runs the full Sailwright quick-start lifecycle for one target and records it.

Options:
  --os NAME          Guest OS (default: ubuntu)
  --type TYPE        Ubuntu type: server|desktop (default: server; ignored for non-ubuntu)
  --arch ARCH        Architecture: amd64|arm64 (default: amd64)
  --skip-install     Skip `sailwright install` (dependencies already present)
  --skip-build       Skip `sailwright build` (reuse an existing/pulled artifact)
  --no-cache         Force a rebuild even if the build artifact already exists
  --keep             Do not stop+destroy the VM at the end (leave it for inspection)
  --no-record        Disable all recording (plain run)
  --record-dir DIR   Directory for recording artifacts (default: ./artifact)
  -h, --help         Show this help

Examples:
  scripts/quickstart.sh
  scripts/quickstart.sh --type desktop --keep
  scripts/quickstart.sh --no-cache
  scripts/quickstart.sh --skip-install --skip-build --record-dir ./artifact
EOF
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
	case "$1" in
	--os)
		OS="$2"
		shift 2
		;;
	--type)
		TYPE="$2"
		shift 2
		;;
	--arch)
		ARCH="$2"
		shift 2
		;;
	--skip-install)
		SKIP_INSTALL="true"
		shift
		;;
	--skip-build)
		SKIP_BUILD="true"
		shift
		;;
	--no-cache)
		NO_CACHE="true"
		shift
		;;
	--keep)
		KEEP="true"
		shift
		;;
	--no-record)
		RECORD="false"
		shift
		;;
	--record-dir)
		RECORD_DIR="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "Unknown option: $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

script_dir=$(
	cd "$(dirname "$0")"
	pwd -P
)
project_root=$(
	cd "${script_dir}/.."
	pwd -P
)

slug="${OS}-${TYPE}-${ARCH}"
if [[ "${OS}" != "ubuntu" ]]; then
	slug="${OS}-${ARCH}"
fi

# ---------------------------------------------------------------------------
# Terminal-recording layer: re-exec this script inside asciinema (preferred)
# or `script` (fallback) so the whole CLI session is captured. The env guard
# prevents an infinite re-exec loop.
# ---------------------------------------------------------------------------
if [[ "${RECORD}" == "true" && "${QUICKSTART_IN_RECORDING:-}" != "1" ]]; then
	mkdir -p "${RECORD_DIR}"
	export QUICKSTART_IN_RECORDING=1
	# The recorder gives `sailwright build` a pty even when nobody can answer its
	# prebuilt-image prompt (e.g. in CI), so skip the prompt in that case.
	if [[ ! -t 0 ]]; then
		export SAILWRIGHT_NO_PREBUILT_OFFER=1
	fi
	# Expand ORIG_ARGS defensively: on bash < 4.4 `"${arr[@]}"` on an empty
	# array trips `set -u`, which would break a no-arg invocation.
	child_cmd="$(printf '%q ' "$0" ${ORIG_ARGS[@]+"${ORIG_ARGS[@]}"})"
	if command -v asciinema >/dev/null 2>&1; then
		cast="${RECORD_DIR}/quickstart-${slug}.cast"
		echo "🎬 Recording terminal session to ${cast} (asciinema)"
		# `asciinema rec` exits 0 whenever the recording itself succeeds,
		# regardless of the recorded command's exit status (verified with
		# asciinema 2.4.0), so it can't be `exec`'d directly like the
		# `script` fallback below. Smuggle the real exit code out via a
		# file the recorded shell writes to once the child finishes.
		rc_file="$(mktemp "${TMPDIR:-/tmp}/quickstart-rc.XXXXXX")"
		trap 'rm -f "${rc_file}"' EXIT
		recorded_cmd="${child_cmd}; echo \$? >$(printf '%q' "${rc_file}")"
		asciinema rec --overwrite --command "${recorded_cmd}" "${cast}"
		rc="$(cat "${rc_file}" 2>/dev/null)"
		exit "${rc:-1}"
	elif command -v script >/dev/null 2>&1; then
		typescript="${RECORD_DIR}/quickstart-${slug}.typescript"
		echo "🎬 Recording terminal session to ${typescript} (asciinema not found; using 'script')."
		echo "   Tip: on the first run asciinema is installed during the install step, so a"
		echo "   later run (or one with --skip-install) records a cleaner .cast instead."
		exec script -q -e -c "${child_cmd}" "${typescript}"
	else
		echo "⚠️ Neither asciinema nor script is available; continuing without terminal recording." >&2
	fi
fi

# Timestamp reference used to collect build-phase recordings produced during
# this run. Created only in the working process (past the re-exec guard above).
START_MARKER="$(mktemp "${TMPDIR:-/tmp}/quickstart-start.XXXXXX")"

# ---------------------------------------------------------------------------
# Logging helpers
# ---------------------------------------------------------------------------
banner() {
	echo ""
	echo "=============================================================================="
	echo "➡️  $*"
	echo "=============================================================================="
}

warn() { echo "⚠️  $*" >&2; }

# ---------------------------------------------------------------------------
# Resolve the sailwright binary. Prefer a built binary; fall back to `go run`.
# ---------------------------------------------------------------------------
SAIL=()
resolve_sailwright() {
	local goos goarch bin
	if command -v go >/dev/null 2>&1; then
		goos="$(cd "${project_root}" && go env GOOS)"
		goarch="$(cd "${project_root}" && go env GOARCH)"
		bin="${project_root}/dist/${goos}-${goarch}/sailwright"
		banner "Building sailwright CLI (make build-cli-local)"
		if make -C "${project_root}" build-cli-local && [[ -x "${bin}" ]]; then
			SAIL=("${bin}")
			echo "Using built binary: ${bin}"
			return
		fi
		warn "make build-cli-local failed or binary missing; falling back to 'go run ./cmd/main.go'"
		SAIL=("go" "run" "./cmd/main.go")
		return
	fi
	echo "❌ Go toolchain not found; cannot build or run sailwright." >&2
	exit 1
}

sail() {
	(cd "${project_root}" && "${SAIL[@]}" "$@")
}

# Common target flags. --type is only meaningful for ubuntu.
common_args() {
	local args=(--arch "${ARCH}")
	if [[ "${OS}" == "ubuntu" ]]; then
		args=(--type "${TYPE}" "${args[@]}")
	fi
	printf '%s\n' "${args[@]}"
}

# ---------------------------------------------------------------------------
# VM graphical-display recording (libvirt domain -> VNC -> mp4).
#
# Mirrors pkg/build/vnc_recording.go: capture frames with vncsnapshot, then
# combine them into an mp4 with ffmpeg. Best-effort: never fails the run.
# ---------------------------------------------------------------------------
VNC_PID=""
FRAME_DIR=""
VNC_OUT=""

libvirt_domain() {
	# Matches LinuxLibvirtDomainName: <os>[-<type>]-<arch>-dev-alchemy
	echo "${slug}-dev-alchemy"
}

start_vm_recording() {
	[[ "${RECORD}" == "true" ]] || return 0
	if [[ "$(uname -s)" != "Linux" ]]; then
		warn "VM display recording only supported on Linux hosts; skipping."
		return 0
	fi
	if ! command -v virsh >/dev/null 2>&1 || ! command -v vncsnapshot >/dev/null 2>&1 || ! command -v ffmpeg >/dev/null 2>&1; then
		warn "virsh/vncsnapshot/ffmpeg not all available; skipping VM display recording."
		return 0
	fi

	local uri domain display target
	uri="${DEV_ALCHEMY_LIBVIRT_URI:-qemu:///system}"
	domain="$(libvirt_domain)"

	display="$(virsh --connect "${uri}" vncdisplay "${domain}" 2>/dev/null | tr -d '[:space:]' | head -n1)"
	if [[ -z "${display}" ]]; then
		warn "Could not resolve a VNC display for libvirt domain '${domain}' on ${uri}; skipping VM display recording."
		return 0
	fi
	# Normalize ":N" -> "127.0.0.1:N" for vncsnapshot.
	if [[ "${display}" == :* ]]; then
		target="127.0.0.1${display}"
	else
		target="${display}"
	fi

	FRAME_DIR="$(mktemp -d "${TMPDIR:-/tmp}/quickstart-vnc.XXXXXX")"
	VNC_OUT="${RECORD_DIR}/quickstart-${slug}.vnc.mp4"
	banner "Starting VM display recording of ${domain} (${target}) -> ${VNC_OUT}"
	# Drive the 1 fps capture ourselves — one vncsnapshot call per numbered
	# frame — instead of relying on vncsnapshot's movie mode, whose multi-frame
	# filename handling is unreliable (a name without a counter overwrites a
	# single file). Mirrors pkg/build/vnc_recording.go, which also loops single
	# snapshots. Capped so a stuck run cannot fill the disk (~10h at 1 fps).
	vnc_capture_loop "${target}" "${FRAME_DIR}" &
	VNC_PID=$!
}

# Background frame grabber for start_vm_recording. Writes zero-padded,
# lexically-sortable frames so ffmpeg's glob picks them up in order.
vnc_capture_loop() {
	local target="$1" frame_dir="$2" i=0 frame
	while [[ ${i} -lt 36000 ]]; do
		printf -v frame '%s/frame-%06d.jpg' "${frame_dir}" "${i}"
		vncsnapshot -quiet "${target}" "${frame}" >/dev/null 2>&1 || true
		i=$((i + 1))
		sleep 1
	done
}

stop_vm_recording() {
	[[ -n "${VNC_PID}" ]] || return 0
	kill "${VNC_PID}" >/dev/null 2>&1 || true
	wait "${VNC_PID}" 2>/dev/null || true
	VNC_PID=""

	if [[ -n "${FRAME_DIR}" ]] && compgen -G "${FRAME_DIR}/*.jpg" >/dev/null 2>&1; then
		mkdir -p "${RECORD_DIR}"
		banner "Encoding VM display recording -> ${VNC_OUT}"
		if ! ffmpeg -y -hide_banner -loglevel warning \
			-framerate 1 -pattern_type glob -i "${FRAME_DIR}/*.jpg" \
			-c:v libx264 -preset veryfast -crf 28 -pix_fmt yuv420p -movflags +faststart \
			"${VNC_OUT}"; then
			warn "ffmpeg failed to encode the VM display recording."
		fi
	else
		warn "No VNC frames were captured; no VM display recording produced."
	fi
	[[ -n "${FRAME_DIR}" ]] && rm -rf "${FRAME_DIR}"
	FRAME_DIR=""
}

# The packer build step records its own VM display to *.vnc.mp4 under the
# managed cache dir (see pkg/build/vnc_recording.go). Collect any produced
# during this run into the artifact dir so the build phase is captured too.
collect_build_recordings() {
	[[ "${RECORD}" == "true" ]] || return 0
	[[ -n "${START_MARKER:-}" && -e "${START_MARKER}" ]] || return 0
	command -v find >/dev/null 2>&1 || return 0

	local raw_roots=() roots=() r canon seen dup mp4 dest n=0
	[[ -n "${DEV_ALCHEMY_CACHE_DIR:-}" ]] && raw_roots+=("${DEV_ALCHEMY_CACHE_DIR}")
	[[ -n "${DEV_ALCHEMY_APP_DATA_DIR:-}" ]] && raw_roots+=("${DEV_ALCHEMY_APP_DATA_DIR}/cache")
	raw_roots+=("${HOME}/.local/share/dev-alchemy/cache" "${project_root}/.dev-alchemy/cache")

	# De-duplicate roots that resolve to the same directory. In CI several of
	# these env-derived paths point at the same cache dir (e.g. CACHE_DIR ==
	# APP_DATA_DIR/cache == project_root/.dev-alchemy/cache); without this each
	# build recording would be copied — and mis-numbered — once per alias.
	for r in "${raw_roots[@]}"; do
		canon="$(realpath -m "${r}" 2>/dev/null || echo "${r}")"
		dup="false"
		for seen in ${roots[@]+"${roots[@]}"}; do
			[[ "${seen}" == "${canon}" ]] && {
				dup="true"
				break
			}
		done
		[[ "${dup}" == "true" ]] || roots+=("${canon}")
	done

	mkdir -p "${RECORD_DIR}"
	for r in "${roots[@]}"; do
		[[ -d "${r}" ]] || continue
		while IFS= read -r -d '' mp4; do
			n=$((n + 1))
			if [[ ${n} -eq 1 ]]; then
				dest="${RECORD_DIR}/build-${slug}.vnc.mp4"
			else
				dest="${RECORD_DIR}/build-${slug}-${n}.vnc.mp4"
			fi
			cp -f "${mp4}" "${dest}" 2>/dev/null || true
		done < <(find "${r}" -name '*.vnc.mp4' -newer "${START_MARKER}" -print0 2>/dev/null)
	done
	if [[ ${n} -gt 0 ]]; then
		echo "🎞️  Collected ${n} build-phase VNC recording(s) into ${RECORD_DIR}."
	fi
	return 0
}

# ---------------------------------------------------------------------------
# Cleanup: always stop recording and finalize the video; stop+destroy the VM
# unless --keep. Runs on normal exit and on any error (set -e / trap).
# ---------------------------------------------------------------------------
cleanup() {
	local rc=$?
	set +e
	stop_vm_recording
	collect_build_recordings
	[[ -n "${START_MARKER:-}" ]] && rm -f "${START_MARKER}"
	if [[ "${KEEP}" != "true" ]]; then
		banner "Cleanup: stop + destroy ${OS} (${slug})"
		mapfile -t cargs < <(common_args)
		sail stop "${OS}" "${cargs[@]}" || warn "stop failed (continuing)"
		sail destroy "${OS}" "${cargs[@]}" || warn "destroy failed (continuing)"
	else
		echo "🔖 --keep set: leaving VM '${OS}' (${slug}) running."
	fi
	exit "${rc}"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Run the workflow
# ---------------------------------------------------------------------------
banner "Sailwright quick-start: os=${OS} type=${TYPE} arch=${ARCH} (skip-install=${SKIP_INSTALL}, skip-build=${SKIP_BUILD}, keep=${KEEP}, record=${RECORD})"
resolve_sailwright
mapfile -t CARGS < <(common_args)

if [[ "${SKIP_INSTALL}" != "true" ]]; then
	banner "Step: install host dependencies"
	sail install
else
	echo "⏭️  Skipping install (--skip-install)."
fi

if [[ "${SKIP_BUILD}" != "true" ]]; then
	banner "Step: build ${OS} artifact"
	build_args=("${CARGS[@]}")
	if [[ "${NO_CACHE}" == "true" ]]; then
		build_args+=(--no-cache)
		echo "♻️  --no-cache: forcing a rebuild even if the artifact exists."
	fi
	sail build "${OS}" "${build_args[@]}"
else
	echo "⏭️  Skipping build (--skip-build)."
	if [[ "${NO_CACHE}" == "true" ]]; then
		warn "--no-cache has no effect with --skip-build (build is skipped)."
	fi
fi

banner "Step: create ${OS} VM"
# Ensure a clean slate: a leftover domain from a previous run makes `create`
# abort with "... already exists". Best-effort; harmless when nothing exists.
if sail destroy "${OS}" "${CARGS[@]}" >/dev/null 2>&1; then
	echo "🧹 Removed a pre-existing ${OS} VM before create."
fi
sail create "${OS}" "${CARGS[@]}"

banner "Step: start ${OS} VM"
sail start "${OS}" "${CARGS[@]}"

# Begin recording the VM's screen now that the domain is running.
start_vm_recording

banner "Step: provision ${OS} (--check / dry-run)"
sail provision "${OS}" "${CARGS[@]}" --check

banner "Step: provision ${OS} (apply)"
sail provision "${OS}" "${CARGS[@]}"

# Stop the display recording before cleanup encodes/deletes nothing further.
stop_vm_recording

banner "✅ Quick-start workflow completed successfully for ${OS} (${slug})"
# cleanup() runs on EXIT (stop/destroy unless --keep).
