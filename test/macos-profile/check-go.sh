#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
export GOOS=darwin GOARCH=arm64 CGO_ENABLED=0
unset RUN_INTEGRATION_TESTS RUN_MACOS_UTM_DEPLOY_SMOKE
output="$(mktemp -d /tmp/sw-go.XXXXXX)"
trap 'rm -rf "$output"' EXIT
go vet ./...
# Discover packages from Go build constraints and OS-specific filenames,
# including Darwin tests. Compile all matching packages, never execute them.
go list -json ./... | python3 -c '
import json,sys
text=sys.stdin.read(); decoder=json.JSONDecoder()
while text.strip():
    item,end=decoder.raw_decode(text.lstrip()); text=text.lstrip()[end:]
    files=sum((item.get(key,[]) for key in ("GoFiles","TestGoFiles","XTestGoFiles")),[])
    from pathlib import Path
    if any("darwin" in name or "//go:build darwin" in (Path(item["Dir"])/name).read_text() or "// +build darwin" in (Path(item["Dir"])/name).read_text() for name in files):
        print(item["ImportPath"])
' > "$output/packages"
while IFS= read -r package; do
    go test -c -o "$output/$(printf '%s' "$package" | tr / _).test" "$package"
done < "$output/packages"
