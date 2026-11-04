#!/usr/bin/env bash
# Reclaims disk space on GitHub-hosted Linux runners by removing large unused
# pre-installed toolsets (.NET, Android SDK, Haskell/GHC, CodeQL, etc.) and
# optionally pruning pre-cached Docker images.
#
# DESTRUCTIVE: deletes system directories and all Docker data. It refuses to
# run outside ephemeral GitHub-hosted runners unless FREE_DISK_SPACE_FORCE=1.
#
# Environment:
#   FREE_DISK_SPACE_PRUNE_DOCKER  "true" (default) prunes Docker images,
#                                 build caches and volumes; "false" skips it.
#   FREE_DISK_SPACE_FORCE         "1" bypasses the GitHub-hosted runner check.
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "Disk space reclamation is only supported on Linux runners; skipping."
	exit 0
fi

if [[ "${RUNNER_ENVIRONMENT:-}" != "github-hosted" && "${FREE_DISK_SPACE_FORCE:-}" != "1" ]]; then
	echo "::warning::Not a GitHub-hosted runner (RUNNER_ENVIRONMENT='${RUNNER_ENVIRONMENT:-}'); skipping destructive disk cleanup. Set FREE_DISK_SPACE_FORCE=1 to override."
	exit 0
fi

if [[ "${EUID}" -eq 0 ]]; then
	SUDO=""
elif command -v sudo >/dev/null 2>&1; then
	SUDO="sudo"
else
	echo "::warning::Neither root nor sudo is available; skipping disk cleanup."
	exit 0
fi

echo "=== Runner disk space before reclamation ==="
df -h /

echo "Removing large unused SDKs and toolchains..."
# GitHub-hosted Ubuntu runners preinstall ~20-30 GB of SDKs that are not
# required by the Sailwright build and test workflows.
${SUDO} rm -rf \
	/usr/local/lib/android \
	/usr/share/dotnet \
	/opt/ghc \
	/usr/local/.ghcup \
	/usr/local/share/boost \
	/usr/share/swift \
	/opt/hostedtoolcache/CodeQL

if [[ "${FREE_DISK_SPACE_PRUNE_DOCKER:-true}" == "true" ]]; then
	if command -v docker >/dev/null 2>&1; then
		echo "Pruning pre-cached Docker images and build caches..."
		${SUDO} docker system prune -af --volumes || true
	fi
else
	echo "Skipping Docker prune (FREE_DISK_SPACE_PRUNE_DOCKER=${FREE_DISK_SPACE_PRUNE_DOCKER})."
fi

echo "Cleaning apt package cache..."
if command -v apt-get >/dev/null 2>&1; then
	${SUDO} apt-get clean || true
fi

echo "=== Runner disk space after reclamation ==="
df -h /
