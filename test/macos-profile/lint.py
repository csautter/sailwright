#!/usr/bin/env python3
"""Reject common GNU-only shell commands in scripts executed on macOS."""
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
FILES = sorted((ROOT / 'scripts/macos').glob('*.sh')) + [
    ROOT / 'build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh',
    ROOT / 'build/packer/windows/windows11-on-macos.sh',
    ROOT / 'build/packer/windows/windows11-qemu.sh',
    ROOT / 'deployments/utm/create-utm-vm.sh',
]
# Exact lines, so an exception cannot hide a new command elsewhere in a file.
# probe_kvm_with_qemu is only reached by the Linux branch of the shared script.
ALLOW = {
    ('build/packer/windows/windows11-qemu.sh', 'if command -v timeout >/dev/null 2>&1; then'),
    ('build/packer/windows/windows11-qemu.sh', 'timeout 3s "$qemu_binary" "${probe_args[@]}" >/dev/null 2>&1'),
}
PATTERN = re.compile(r'\b(?:stat\s+-c\S*|sed\s+-i(?:\s|$)|readlink\s+-f\b|date\s+-d\b|grep\s+-P\b|timeout\s|command -v timeout\b|mktemp\s+--suffix\b|sort\s+-V\b|realpath\s+--\w+)')
SUFFIX = re.compile(r'mktemp.*XXXXXX\.[^\s\"]+')
errors = []
for path in FILES:
    subprocess.run(['bash', '-n', str(path)], check=True)
    for number, line in enumerate(path.read_text().splitlines(), 1):
        line = line.strip()
        if line.startswith('#'):
            continue
        if (PATTERN.search(line) or SUFFIX.search(line)) and (str(path.relative_to(ROOT)), line) not in ALLOW:
            errors.append(f'{path.relative_to(ROOT)}:{number}: GNU-only command: {line}')
if errors:
    raise SystemExit('\n'.join(errors))
print(f'macOS portability lint passed ({len(FILES)} scripts).')
