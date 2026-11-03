#!/usr/bin/env bash
# Reclaims disk space on GitHub-hosted Linux runners by removing large unused
# pre-installed toolsets (.NET, Android SDK, Haskell/GHC, CodeQL, etc.) and
# pruning pre-cached Docker images.
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "Disk space reclamation is only supported on Linux runners; skipping."
	exit 0
fi

if [[ "${EUID}" -eq 0 ]]; then
	SUDO=""
elif command -v sudo >/dev/null 2>&1; then
	SUDO="sudo"
else
	SUDO=""
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

if command -v docker >/dev/null 2>&1; then
	echo "Pruning pre-cached Docker images and build caches..."
	${SUDO} docker system prune -af --volumes || true
fi

echo "Cleaning apt package cache..."
if command -v apt-get >/dev/null 2>&1; then
	${SUDO} apt-get clean || true
fi

echo "=== Runner disk space after reclamation ==="
df -h /
