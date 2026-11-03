#!/usr/bin/env bash

# Shared by the dependency installers; the CLI reads the same version file.
ANSIBLE_CORE_MIN_VERSION="$(cat "$(dirname "${BASH_SOURCE[0]}")/ansible-core-min-version")"

check_ansible_core() {
	local python="$1" output
	if ! output="$(ansible-playbook --version 2>&1)"; then
		printf '%s\n' "${output}" >&2
		echo "ansible-playbook could not start. Rerun sailwright install and check which ansible-playbook is on PATH." >&2
		return 1
	fi
	printf '%s\n' "${output}"
	"${python}" - "${ANSIBLE_CORE_MIN_VERSION}" "${output}" <<'PY'
import re
import sys

minimum, output = sys.argv[1:]
match = re.search(r"\[core (\d+)\.(\d+)\.(\d+)([^\]]*)\]", output)
if not match or match[4] or tuple(map(int, match.group(1, 2, 3))) < tuple(map(int, minimum.split('.'))):
    sys.exit(
        f"Sailwright requires ansible-core >= {minimum}. "
        "Rerun sailwright install, remove an older Ansible from PATH, or activate "
        "an environment with a supported ansible-core; check with command -v ansible-playbook."
    )
PY
}
