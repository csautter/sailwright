#!/usr/bin/env python3
"""Create all six UTM bundles using disposable disk fixtures."""
import argparse
import difflib
import importlib.util
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent

_spec = importlib.util.spec_from_file_location('boot_utm', HERE / 'boot-utm.py')
boot_utm = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(boot_utm)


class UTMUnavailable(Exception):
    """UTM does not answer Apple Events here, so registration cannot be checked."""


def utm_answers():
    """Raise UTMUnavailable unless UTM answers a read-only Apple Event."""
    try:
        boot_utm.applescript(['tell application "UTM" to get id of virtual machines'])
    except subprocess.TimeoutExpired as error:
        raise UTMUnavailable(f'UTM did not answer Apple Events within {error.timeout:g}s') from error
    except (OSError, subprocess.CalledProcessError) as error:
        detail = (getattr(error, 'stderr', '') or '').strip() or str(error)
        raise UTMUnavailable(f'UTM does not answer Apple Events: {detail}') from error


def verify_registration(bundle, identifier):
    """Register the bundle and require UTM to list its UUID at this path, then unregister it."""
    boot_utm.register_bundle(bundle)
    try:
        registered = boot_utm.wait_for_registration(identifier)
    except boot_utm.RegistrationTimeout as error:
        raise AssertionError(f'{error}; UTM lists: {boot_utm.list_utm_vms()}') from error
    if Path(registered).resolve() != bundle.resolve():
        raise AssertionError(f'UTM lists {identifier} at {registered}, expected {bundle}')
    # Only the disposable fixture is deleted: its UUID is fresh and its path matched.
    boot_utm.vm_command(identifier, 'delete targetVM')


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
    utm_skip = None
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
                if args.register and utm_skip is None:
                    try:
                        utm_answers()
                    except UTMUnavailable as error:
                        # Hosted runners cannot drive UTM; say so instead of passing.
                        utm_skip = str(error)
                        print(f'UTM registration SKIPPED for all bundles: {utm_skip}')
                    else:
                        verify_registration(bundle, config['Information']['UUID'])
                        print(f'UTM registration passed: {system}/{arch} listed at its bundle path')
                print(f'OK: UTM {system}/{arch}')


if __name__ == '__main__':
    main()
