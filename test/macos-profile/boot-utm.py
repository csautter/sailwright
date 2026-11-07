#!/usr/bin/env python3
"""Boot a disposable UTM bundle, require a guest-agent IP, then stop and delete it."""
import argparse
import ipaddress
import json
from pathlib import Path
import plistlib
import re
import subprocess
import time


def applescript(lines, *args):
    command = ['osascript']
    for line in ['on run argv', *lines, 'end run']:
        command.extend(['-e', line])
    return subprocess.run(command + list(args), check=True, capture_output=True,
                          text=True, timeout=60).stdout.strip()


def vm_command(identifier, action):
    return applescript(['tell application "UTM"',
                        'set targetVM to virtual machine id (item 1 of argv)',
                        action, 'end tell'], identifier)


def guest_addresses(output):
    addresses = []
    for value in re.split(r'[,\s]+', output):
        try:
            address = ipaddress.ip_address(value)
        except ValueError:
            continue
        if not (address.is_loopback or address.is_link_local or address.is_unspecified):
            addresses.append(str(address))
    return addresses


def boot(bundle, timeout, diagnostics, software_emulation=False):
    diagnostics.mkdir(parents=True, exist_ok=True)
    result = dict(bundle=str(bundle), status='not started',
                  software_emulation=software_emulation)
    started = False
    try:
        config_path = bundle / 'config.plist'
        config = plistlib.loads(config_path.read_bytes())
        identifier = config['Information']['UUID']
        result['uuid'] = identifier
        if software_emulation:
            # Only the disposable CI bundle changes. Nested HVF/vmnet is
            # unavailable on Tart; production templates stay native.
            config['QEMU']['Hypervisor'] = False
            config['System']['MemorySize'] = min(config['System']['MemorySize'], 4096)
            for network in config.get('Network', []):
                network['Mode'] = 'Emulated'
            config_path.write_bytes(plistlib.dumps(config))
        (diagnostics / 'config.plist').write_bytes(config_path.read_bytes())
        # Unlike the optional fixture registration check, every failure here
        # fails CI. Select by UUID to avoid touching same-named user VMs.
        applescript(['tell application "UTM" to open (POSIX file (item 1 of argv))'], str(bundle))
        registered = applescript(['tell application "UTM"',
                                  'return POSIX path of (file of virtual machine id (item 1 of argv))',
                                  'end tell'], identifier)
        if Path(registered).resolve() != bundle.resolve():
            raise RuntimeError('Refusing to smoke-test a UUID registered at another bundle path')
        if vm_command(identifier, 'return (status of targetVM) as text') != 'stopped':
            raise RuntimeError('Refusing to smoke-test an already running VM')
        started = True  # A failing/timed-out start may still have launched QEMU.
        vm_command(identifier, 'start targetVM')
        deadline = time.monotonic() + timeout
        last_error = ''
        while time.monotonic() < deadline:
            status = vm_command(identifier, 'return (status of targetVM) as text')
            result['status'] = status
            if status == 'stopped':
                raise RuntimeError('UTM stopped before the guest became ready')
            if status == 'started':
                try:
                    addresses = guest_addresses(vm_command(identifier, 'return (query ip targetVM)'))
                    if addresses:
                        result['addresses'] = addresses
                        print(f'UTM guest boot passed: {bundle.name}: {addresses}', flush=True)
                        return
                except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
                    # The agent is unavailable while the installed OS boots.
                    last_error = f'{error}; {getattr(error, "stderr", "") or ""}'
            time.sleep(5)
        raise TimeoutError(f'UTM guest agent not ready after {timeout}s; last error: {last_error}')
    except Exception as error:
        result['error'] = str(error)
        if isinstance(error, subprocess.CalledProcessError):
            result['stderr'] = error.stderr
        raise
    finally:
        try:
            if started:
                vm_command(identifier, 'stop targetVM by force')
                stop_deadline = time.monotonic() + 60
                while vm_command(identifier, 'return (status of targetVM) as text') != 'stopped':
                    if time.monotonic() >= stop_deadline:
                        raise RuntimeError('UTM VM did not stop after smoke test')
                    time.sleep(1)
                vm_command(identifier, 'delete targetVM')
        except Exception as error:
            result['cleanup_error'] = str(error)
            raise
        finally:
            (diagnostics / 'result.json').write_text(json.dumps(result, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bundle', type=Path)
    parser.add_argument('--timeout', type=int, default=1200)
    parser.add_argument('--diagnostics', type=Path, required=True)
    parser.add_argument('--software-emulation', action='store_true')
    args = parser.parse_args()
    if args.timeout <= 0:
        parser.error('--timeout must be positive')
    boot(args.bundle.resolve(), args.timeout, args.diagnostics, args.software_emulation)


if __name__ == '__main__':
    main()
