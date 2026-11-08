#!/usr/bin/env python3
"""Validate the real Packer templates and snapshot their Darwin QEMU argv."""
import argparse
import difflib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent


def run_variant(work, arch, variant, update):
    windows = variant == 'windows11'
    source = ROOT / ('build/packer/windows' if windows else 'build/packer/linux/ubuntu')
    template = source / ('windows11-qemu.pkr.hcl' if windows else 'linux-ubuntu-qemu.pkr.hcl')
    # Keep the template unchanged; copy only fixtures it reads at validation/start.
    fixture = work / variant
    shutil.copytree(source, fixture)
    template = fixture / template.name
    cache = fixture / 'cache with spaces'
    firmware = cache / 'qemu-uefi/usr/share/AAVMF'
    firmware.mkdir(parents=True)
    for name in ('AAVMF_CODE.no-secboot.fd', 'AAVMF_VARS.fd'):
        (firmware / name).write_bytes(b'firmware fixture')
    iso = fixture / 'installer.iso'
    iso.write_bytes(b'ISO fixture; never booted')
    for folder, name in [('windows', 'virtio-win.iso'), ('utm', 'utm-guest-tools-latest.iso')]:
        (cache / folder).mkdir()
        (cache / folder / name).write_bytes(b'guest tools fixture')
    if not windows:
        (fixture / f'cloud-init/qemu-{variant}/cidata.iso').write_bytes(b'cidata fixture')
    argv_file = fixture / 'argv.json'
    out = fixture / 'out'
    env = dict(os.environ, CI='true', DEV_ALCHEMY_PACKER_START_ONLY='true',
               MACOS_PROFILE_ARGV=str(argv_file), PACKER_CACHE_DIR=str(work / 'packer-cache'),
               TMPDIR=str(work))
    # Ambient PKR_VAR settings must not silently change the recorded profile.
    env = {key: value for key, value in env.items() if not key.startswith('PKR_VAR_')}
    variables = dict(host_os='darwin', host_arch='arm64', arch=arch, cache_dir=str(cache),
                     build_output_dir=str(out), qmp_socket_path=str(work / 'qmp.sock'),
                     iso_url=str(iso), headless='false', cpus='4', memory='4096')
    if windows:
        # The template requires explicit filenames; use the disposable fixtures.
        variables['utm_guest_tools_iso_name'] = 'utm-guest-tools-latest.iso'
        variables['virtio_win_iso_name'] = 'virtio-win.iso'
    else:
        variables['ubuntu_type'] = variant
    flags = [item for key, value in variables.items() for item in ('-var', f'{key}={value}')]
    subprocess.run(['packer', 'init', str(template)], env=env, check=True, stdout=subprocess.DEVNULL)
    subprocess.run(['packer', 'validate', *flags, str(template)], env=env, check=True)
    with (fixture / 'packer.log').open('w') as log:
        process = subprocess.Popen(['packer', 'build', '-color=false', *flags, str(template)],
                                   env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            deadline = time.monotonic() + 90
            while not argv_file.exists():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError(f'No QEMU argv captured for {variant}/{arch}:\n' + (fixture / 'packer.log').read_text())
                time.sleep(0.1)
        finally:
            os.killpg(process.pid, signal.SIGTERM) if process.poll() is None else None
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
    # Preserve duplicates, repeated-option order and all values. Normalize transient
    # paths and allocated ports, before any possible real-QEMU probe rewriting.
    args = json.loads(argv_file.read_text())
    if '-machine' not in args or '-drive' not in args:
        raise AssertionError(f'Expected a VM invocation, got {args!r}')
    normalized = []
    for arg in args:
        arg = arg.replace(str(fixture), '<FIXTURE>').replace(str(work), '<WORK>')
        arg = re.sub(r'(hostfwd=tcp:[^:]*:)\d+-', r'\1<SSH_PORT>-', arg)
        arg = re.sub(r'(127\.0\.0\.1:)(\d+)(,password)', r'\1<VNC_PORT>\3', arg)
        arg = re.sub(r'<WORK>/packer[0-9]+', '<PACKER_TEMP>', arg)
        normalized.append(arg)
    # The plugin iterates a Go map of option names. Canonicalize that map's
    # order, retaining the order and every value of repeated options.
    if len(normalized) % 2:
        raise AssertionError(f'Expected option/value pairs, got {normalized!r}')
    grouped = {}
    for option, value in zip(normalized[::2], normalized[1::2]):
        if not option.startswith('-'):
            raise AssertionError(f'Unexpected option {option!r}')
        grouped.setdefault(option, []).append(value)
    actual = json.dumps(grouped, indent=2, sort_keys=True) + '\n'
    golden = HERE / 'golden' / f'{"windows11" if windows else "ubuntu-" + variant}-{arch}.json'
    if update:
        golden.write_text(actual)
    elif not golden.exists() or golden.read_text() != actual:
        expected = golden.read_text() if golden.exists() else ''
        raise AssertionError(''.join(difflib.unified_diff(expected.splitlines(True), actual.splitlines(True),
                                                       fromfile=str(golden), tofile='actual')))
    print(f'OK: {variant}/{arch}', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arch', choices=['amd64', 'arm64', 'all'], default='all')
    parser.add_argument('--update', action='store_true')
    args = parser.parse_args()
    for arch in (['amd64', 'arm64'] if args.arch == 'all' else [args.arch]):
        with tempfile.TemporaryDirectory(prefix='sw-mp-', dir='/tmp') as directory:
            work = Path(directory)
            bin_dir = work / 'bin'
            bin_dir.mkdir()
            for binary in ('qemu-system-x86_64', 'qemu-system-aarch64'):
                (bin_dir / binary).symlink_to(HERE / 'qemu-shim.py')
            original_path = os.environ['PATH']
            os.environ['PATH'] = str(bin_dir) + os.pathsep + original_path
            try:
                for variant in ('server', 'desktop', 'windows11'):
                    run_variant(work, arch, variant, args.update)
            finally:
                os.environ['PATH'] = original_path


if __name__ == '__main__':
    main()
