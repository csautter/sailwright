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

START_GRACE_SECONDS = 60
PROBE_TIMEOUT_SECONDS = 10
UTM_BUNDLE_ID = 'com.utmapp.UTM'
DEFAULT_UTM_APP = Path('/Applications/UTM.app')
TCC_DB = Path.home() / 'Library/Application Support/com.apple.TCC/TCC.db'
# GUI processes that own the prompts a headless AppleScript caller waits on.
PROMPT_OWNERS = {
    'CoreServicesUIAgent': 'Gatekeeper first-launch confirmation',
    'UserNotificationCenter': 'privacy consent (TCC) prompt',
}
WINDOW_OWNERS_JXA = '''
ObjC.import("CoreGraphics");
var windows = ObjC.castRefToObject($.CGWindowListCopyWindowInfo(1 | 16, 0));
var lines = [];
for (var i = 0; i < windows.count; i++) {
  var w = windows.objectAtIndex(i);
  lines.push(ObjC.unwrap(w.objectForKey("kCGWindowOwnerName")) + "\\t" +
             ObjC.unwrap(w.objectForKey("kCGWindowLayer")));
}
lines.join("\\n");
'''


class UTMNotResponding(RuntimeError):
    """UTM did not answer Apple Events; the message carries the observed UTM state."""


def probe(command):
    """Run a read-only diagnostic command; never raise."""
    try:
        completed = subprocess.run(command, capture_output=True, text=True,
                                   timeout=PROBE_TIMEOUT_SECONDS)
    except (OSError, subprocess.SubprocessError) as error:
        return None, str(error)
    if completed.returncode != 0:
        return completed, (completed.stderr or '').strip() or f'exit {completed.returncode}'
    return completed, None


def utm_processes():
    completed, error = probe(['ps', '-axo', 'pid=,stat=,comm='])
    if error:
        return f'unknown: {error}'
    processes = []
    for line in completed.stdout.splitlines():
        fields = line.split(None, 2)
        if len(fields) == 3 and fields[2].endswith('.app/Contents/MacOS/UTM'):
            processes.append({'pid': int(fields[0]), 'state': fields[1], 'path': fields[2]})
    return processes


def utm_quarantine(app):
    completed, error = probe(['xattr', '-p', 'com.apple.quarantine', str(app)])
    if completed is None:
        return f'unknown: {error}'
    return completed.stdout.strip() if completed.returncode == 0 else None


def prompt_windows():
    completed, error = probe(['osascript', '-l', 'JavaScript', '-e', WINDOW_OWNERS_JXA])
    if error:
        return f'unknown: {error}'
    windows = []
    for line in completed.stdout.splitlines():
        owner, _, layer = line.partition('\t')
        if owner in PROMPT_OWNERS and layer.lstrip('-').isdigit() and int(layer) > 0:
            windows.append({'owner': owner, 'layer': int(layer), 'meaning': PROMPT_OWNERS[owner]})
    return windows


def automation_grants():
    query = ("SELECT client, auth_value FROM access WHERE service = 'kTCCServiceAppleEvents' "
             f"AND indirect_object_identifier = '{UTM_BUNDLE_ID}'")
    completed, error = probe(['sqlite3', '-readonly', '-separator', '\t', str(TCC_DB), query])
    if error:
        return f'unknown: {error}'
    grants = []
    for line in completed.stdout.splitlines():
        client, _, value = line.partition('\t')
        if value.isdigit():
            grants.append({'client': client, 'auth_value': int(value)})
    return grants


def diagnose_utm():
    """Collect why UTM may ignore Apple Events, without sending any to it."""
    processes = utm_processes()
    app = DEFAULT_UTM_APP
    if isinstance(processes, list) and processes:
        app = Path(processes[0]['path']).parents[2]
    state = dict(processes=processes, app=str(app), quarantine=utm_quarantine(app),
                 prompt_windows=prompt_windows(), automation_grants=automation_grants())
    hints = []
    if isinstance(processes, list):
        if not processes:
            hints.append('UTM is not running')
        elif any(p['state'].startswith('T') for p in processes):
            hints.append('UTM is suspended (process state T): LaunchServices holds it until '
                         "Gatekeeper's first-launch confirmation is answered")
    if state['quarantine'] and not state['quarantine'].startswith('unknown'):
        hints.append(f'{app.name} carries com.apple.quarantine; first launch needs a GUI '
                     'confirmation (clear it with .github/runners/prepare-utm-automation.sh)')
    if isinstance(state['prompt_windows'], list):
        hints.extend(f"{w['owner']} shows a window: {w['meaning']}" for w in state['prompt_windows'])
    grants = state['automation_grants']
    if isinstance(grants, list):
        if not grants:
            hints.append('no Automation (Apple Events) grant for UTM in the user TCC database; '
                         'macOS waits for GUI consent')
        elif not any(g['auth_value'] == 2 for g in grants):
            hints.append('Automation (Apple Events) for UTM is denied in the user TCC database')
    state['hints'] = hints or ['no known blocker found; UTM may still be starting']
    return state


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
        try:
            applescript(['tell application "UTM" to open (POSIX file (item 1 of argv))'], str(bundle))
        except subprocess.TimeoutExpired as error:
            result['utm_state'] = diagnose_utm()
            raise UTMNotResponding(
                f'UTM did not answer the first AppleScript call within {error.timeout:g}s: '
                + '; '.join(result['utm_state']['hints'])) from error
        registered = applescript(['tell application "UTM"',
                                  'return POSIX path of (file of virtual machine id (item 1 of argv))',
                                  'end tell'], identifier)
        if Path(registered).resolve() != bundle.resolve():
            raise RuntimeError('Refusing to smoke-test a UUID registered at another bundle path')
        if vm_command(identifier, 'return (status of targetVM) as text') != 'stopped':
            raise RuntimeError('Refusing to smoke-test an already running VM')
        started = True  # A failing/timed-out start may still have launched QEMU.
        vm_command(identifier, 'start targetVM')
        started_at = time.monotonic()
        deadline = started_at + timeout
        last_error = ''
        left_stopped = False
        while time.monotonic() < deadline:
            status = vm_command(identifier, 'return (status of targetVM) as text')
            result['status'] = status
            if status == 'stopped':
                # UTM may report the old state briefly after accepting start.
                # Fail once the VM ran and stopped, or never left stopped.
                if left_stopped or time.monotonic() - started_at >= START_GRACE_SECONDS:
                    raise RuntimeError('UTM stopped before the guest became ready')
                time.sleep(1)
                continue
            left_stopped = True
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
        if isinstance(error, subprocess.TimeoutExpired) and 'utm_state' not in result:
            result['utm_state'] = diagnose_utm()
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
