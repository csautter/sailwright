#!/usr/bin/env python3
"""Create all six UTM bundles using disposable disk fixtures."""
import argparse
import difflib
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent


def normalize(value):
    if isinstance(value, dict):
        return {key: normalize(item) for key, item in value.items()}
    if isinstance(value, list):
        return [normalize(item) for item in value]
    if isinstance(value, str):
        value = re.sub(r'[0-9A-Fa-f]{8}(?:-[0-9A-Fa-f]{4}){3}-[0-9A-Fa-f]{12}', '<UUID>', value)
        value = re.sub(r'(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}', '<MAC>', value)
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--update', action='store_true')
    parser.add_argument('--register', action='store_true', help='Try UTM registration on a real Mac, never boot a guest')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='sw-utm-') as directory:
        work = Path(directory)
        cache = work / 'Application Support/cache'
        bundles = work / 'UTM Bundles'
        for arch in ('amd64', 'arm64'):
            for system in ('windows11', 'ubuntu-server', 'ubuntu-desktop'):
                disk = cache / (f'windows11/qemu-windows11-{arch}.qcow2' if system == 'windows11' else f'ubuntu/qemu-{system}-packer-{arch}.qcow2')
                disk.parent.mkdir(parents=True, exist_ok=True)
                subprocess.run(['qemu-img', 'create', '-f', 'qcow2', str(disk), '1M'], check=True, stdout=subprocess.DEVNULL)
                env = dict(os.environ, DEV_ALCHEMY_CACHE_DIR=str(cache), SAILWRIGHT_UTM_VM_DIR=str(bundles))
                subprocess.run(['bash', str(ROOT / 'deployments/utm/create-utm-vm.sh'), '--arch', arch, '--os', system], env=env, check=True, stdout=subprocess.DEVNULL)
                bundle = bundles / f'{system}-{arch}-dev-alchemy.utm'
                config = plistlib.loads((bundle / 'config.plist').read_bytes())
                actual = json.dumps(normalize(config), indent=2, sort_keys=True) + '\n'
                golden = HERE / 'golden' / f'utm-{system}-{arch}.json'
                if args.update:
                    golden.write_text(actual)
                elif not golden.exists() or golden.read_text() != actual:
                    expected = golden.read_text() if golden.exists() else ''
                    raise AssertionError(''.join(difflib.unified_diff(expected.splitlines(True), actual.splitlines(True), fromfile=str(golden), tofile='actual')))
                copied = bundle / 'Data' / f'{system}-{arch}.qcow2'
                assert copied.read_bytes() == disk.read_bytes(), copied
                # Existing configs/images must survive a second invocation.
                original = (bundle / 'config.plist').read_bytes()
                subprocess.run(['bash', str(ROOT / 'deployments/utm/create-utm-vm.sh'), '--arch', arch, '--os', system], env=env, check=True, stdout=subprocess.DEVNULL)
                assert original == (bundle / 'config.plist').read_bytes()
                if args.register:
                    try:
                        # argv avoids quoting paths into AppleScript source.
                        result = subprocess.run(['osascript', '-e', 'on run argv', '-e', 'tell application "UTM" to open (POSIX file (item 1 of argv))', '-e', 'end run', str(bundle)], capture_output=True, text=True, timeout=30)
                        if result.returncode:
                            print(f'UTM registration unavailable: {result.stderr.strip()}; bundle validation passed.')
                        else:
                            print(f'UTM registration passed: {system}/{arch}')
                    except subprocess.TimeoutExpired:
                        print('UTM registration timed out; bundle validation passed.')
                print(f'OK: UTM {system}/{arch}')


if __name__ == '__main__':
    main()
