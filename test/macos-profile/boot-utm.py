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
# Without a hypervisor UTM runs an aarch64 guest with the CPU model "default"
# as cortex-a72 (ARMv8.0); the Windows 11 kernel parks the boot CPU on it.
# With neoverse-n1 Windows boots, but x64 programs such as the x64-only QEMU
# guest agent from UTM Guest Tools never start under its x64 emulation. The CPU
# Packer installs the image with under TCG runs both. UTM's CPU setting cannot
# carry QEMU CPU properties, so the model goes into AdditionalArguments; QEMU
# applies the last -cpu option. Keep in sync with arm64_cpu_model in
# build/packer/windows/windows11-qemu.pkr.hcl (checked by test-boot-utm.py).
TCG_AARCH64_CPU = 'max,sve=off,sme=off,pauth-impdef=on'
RAMFB_WITH_VIRTIO_GPU = ('virtio-ramfb', 'virtio-ramfb-gl')
REGISTRATION_TIMEOUT_SECONDS = 60
REGISTRATION_POLL_SECONDS = 2
# When UTM does not list the bundle after `open`, it rejected it without an
# AppleScript error; its log says why.
UTM_LOG_SECONDS = 300
UTM_LOG_LINES = 200
UTM_LOG_TIMEOUT_SECONDS = 60
# Every AppleScript poll makes UTM log a TCC access check; drop that noise.
UTM_LOG_PREDICATE = ('process == "UTM" AND NOT (subsystem IN {"com.apple.TCC", "com.apple.xpc", '
                     '"com.apple.FrontBoard", "com.apple.AppKit"})')
# AppleScript "Can't get <object>" (errAENoSuchObject); the text is localized.
UNKNOWN_OBJECT_ERROR = '(-1728)'
# One line per VM: id, name and bundle path, tab-separated. Paths are coerced
# outside the tell block (see registered_path) and may be missing.
LIST_VMS_SCRIPT = [
    'tell application "UTM"',
    'set vmIds to id of virtual machines',
    'set vmNames to name of virtual machines',
    'try',
    'set vmFiles to file of virtual machines',
    'on error',
    'set vmFiles to {}',
    'end try',
    'end tell',
    'set out to {}',
    'repeat with i from 1 to count of vmIds',
    'set vmPath to ""',
    'try',
    'set vmPath to POSIX path of (item i of vmFiles)',
    'end try',
    'set end of out to (item i of vmIds) & tab & (item i of vmNames) & tab & vmPath',
    'end repeat',
    "set AppleScript's text item delimiters to linefeed",
    'return out as text',
]
# Read-only first call: launches UTM and proves it answers Apple Events.
PING_SCRIPT = ['tell application "UTM" to get id of virtual machines']
PROBE_TIMEOUT_SECONDS = 10
UTM_BUNDLE_ID = 'com.utmapp.UTM'
DEFAULT_UTM_APP = Path('/Applications/UTM.app')
TCC_DB = Path.home() / 'Library/Application Support/com.apple.TCC/TCC.db'
# CI jobs run below the runner's SSH session; macOS attributes their Apple
# Events to this sender, so only its grant decides whether UTM answers.
AUTOMATION_CLIENT = '/usr/libexec/sshd-keygen-wrapper'
# GUI processes that own the prompts a headless AppleScript caller waits on.
PROMPT_OWNERS = {
    'CoreServicesUIAgent': 'Gatekeeper first-launch confirmation',
    'UserNotificationCenter': 'privacy consent (TCC) prompt',
}
# Owner and layer only: window titles need a Screen Recording grant, and
# alerts carry none, so the text of a UTM error alert is not visible here.
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


def probe(command, timeout=PROBE_TIMEOUT_SECONDS):
    """Run a read-only diagnostic command; never raise."""
    try:
        completed = subprocess.run(command, capture_output=True, text=True,
                                   timeout=timeout)
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
        if len(fields) == 3 and fields[0].isdigit() and fields[2].endswith('.app/Contents/MacOS/UTM'):
            processes.append({'pid': int(fields[0]), 'state': fields[1], 'path': fields[2]})
    return processes


def utm_quarantine(app):
    completed, error = probe(['xattr', '-p', 'com.apple.quarantine', str(app)])
    if completed is None:
        return f'unknown: {error}'
    return completed.stdout.strip() if completed.returncode == 0 else None


def parse_windows(output):
    """(owner, layer) per on-screen window; malformed lines are skipped."""
    windows = []
    for line in output.splitlines():
        owner, _, rest = line.partition('\t')
        layer = rest.partition('\t')[0]
        if layer.lstrip('-').isdigit():
            windows.append((owner, int(layer)))
    return windows


def on_screen_windows():
    completed, error = probe(['osascript', '-l', 'JavaScript', '-e', WINDOW_OWNERS_JXA])
    if error:
        return f'unknown: {error}'
    return parse_windows(completed.stdout)


def prompt_windows():
    windows = on_screen_windows()
    if isinstance(windows, str):
        return windows
    return [{'owner': owner, 'layer': layer, 'meaning': PROMPT_OWNERS[owner]}
            for owner, layer in windows if owner in PROMPT_OWNERS and layer > 0]


def utm_windows():
    """Layers of UTM's on-screen windows: shows whether UTM has a window or
    alert open, not its text (see WINDOW_OWNERS_JXA)."""
    windows = on_screen_windows()
    if isinstance(windows, str):
        return windows
    return [{'layer': layer} for owner, layer in windows if owner == 'UTM']


def utm_log():
    """Recent UTM log lines (unified log), newest last; never raise."""
    completed, error = probe(['log', 'show', '--last', f'{UTM_LOG_SECONDS}s', '--style', 'compact',
                              '--predicate', UTM_LOG_PREDICATE], timeout=UTM_LOG_TIMEOUT_SECONDS)
    if error:
        return [f'unknown: {error}']
    # Multi-line entries (indented continuation lines, closing braces) are noise.
    lines = [line for line in completed.stdout.splitlines()
             if line.strip() and not line[0].isspace() and line != '}']
    return lines[-UTM_LOG_LINES:]


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
        own = [g for g in grants if g['client'] == AUTOMATION_CLIENT]
        if not own:
            hints.append(f'no Automation (Apple Events) grant for {AUTOMATION_CLIENT} -> UTM in the '
                         'user TCC database; macOS waits for GUI consent')
        elif not any(g['auth_value'] == 2 for g in own):
            hints.append(f'Automation (Apple Events) for {AUTOMATION_CLIENT} -> UTM is denied in the '
                         'user TCC database')
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


class RegistrationTimeout(RuntimeError):
    """UTM accepted `open` but never listed the bundle's UUID."""


class RegisteredElsewhere(RuntimeError):
    """UTM lists the bundle's UUID at another bundle path."""


def ping_utm():
    return applescript(PING_SCRIPT)


def registered_path(identifier):
    # Resolve the file inside the tell block but coerce it outside: within it,
    # UTM receives `POSIX path of (file of ...)` as one specifier and fails
    # with -1700 ("Can't make ... into type specifier").
    return applescript(['tell application "UTM"',
                        'set vmFile to file of virtual machine id (item 1 of argv)',
                        'end tell',
                        'return POSIX path of vmFile'], identifier)


def register_bundle(bundle):
    """Register the bundle in place, as a double-click in Finder does.

    UTM's scripting dictionary has no `open` command: AppleScript
    `open (POSIX file ...)` returns missing value and registers nothing, and
    `import` copies the whole bundle into UTM's container. LaunchServices
    keeps the bundle where it is.
    """
    subprocess.run(['open', '-b', UTM_BUNDLE_ID, str(bundle)], check=True,
                   capture_output=True, text=True, timeout=60)


def wait_for_registration(identifier, timeout=None):
    """Return the bundle path UTM registered for the UUID.

    `open` can return before UTM lists the VM, so an unknown UUID (-1728) is
    retried until the deadline; any other AppleScript error is fatal at once.
    """
    timeout = REGISTRATION_TIMEOUT_SECONDS if timeout is None else timeout
    deadline = None  # Set on the first miss; the usual immediate hit needs no clock.
    while True:
        try:
            return registered_path(identifier)
        except subprocess.CalledProcessError as error:
            if UNKNOWN_OBJECT_ERROR not in (error.stderr or ''):
                raise
            now = time.monotonic()
            if deadline is None:
                deadline = now + timeout
            if now >= deadline:
                raise RegistrationTimeout(
                    f'UTM did not list virtual machine id {identifier} within {timeout:g}s '
                    'after open') from error
        time.sleep(REGISTRATION_POLL_SECONDS)


def same_bundle(registered, bundle):
    # resolve() maps /var and /tmp aliases to /private, as UTM reports them.
    return Path(registered).resolve() == Path(bundle).resolve()


def register_in_place(bundle, identifier):
    """Register the bundle and require UTM to list its UUID at this path.

    Raises RegistrationTimeout or RegisteredElsewhere; returns the listed path.
    """
    register_bundle(bundle)
    registered = wait_for_registration(identifier)
    if not same_bundle(registered, bundle):
        raise RegisteredElsewhere(f'UTM lists {identifier} at {registered}, expected {bundle}')
    return registered


def unregister_in_place(bundle, identifier):
    """Delete the UUID from UTM only if UTM lists it at this bundle path.

    Never raises; returns None when nothing of ours is left registered,
    otherwise why the registration may remain.
    """
    try:
        registered = registered_path(identifier)
    except subprocess.CalledProcessError as error:
        if UNKNOWN_OBJECT_ERROR in (error.stderr or ''):
            return None
        return f'{error}; {(error.stderr or "").strip()}'
    except (OSError, subprocess.SubprocessError) as error:
        return str(error)
    if not same_bundle(registered, bundle):
        return f'not deleting {identifier}: UTM lists it at {registered}, not {bundle}'
    try:
        vm_command(identifier, 'delete targetVM')
    except (OSError, subprocess.SubprocessError) as error:
        return f'{error}; {(getattr(error, "stderr", "") or "").strip()}'
    return None


def parse_utm_vms(output):
    vms = []
    for line in output.splitlines():
        if not line.strip():
            continue
        vm_id, _, rest = line.partition('\t')
        name, _, path = rest.partition('\t')
        vms.append({'id': vm_id, 'name': name, 'path': path or None})
    return vms


def list_utm_vms():
    """Describe every VM UTM knows, for diagnostics; never raise."""
    try:
        return parse_utm_vms(applescript(LIST_VMS_SCRIPT))
    except (OSError, subprocess.SubprocessError) as error:
        return f'unknown: {error}; {getattr(error, "stderr", "") or ""}'.rstrip('; ')


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


def apply_software_emulation(config):
    """Adapt a disposable bundle config to TCG; return what changed and why.

    Nested HVF/vmnet is unavailable on Tart; production templates stay native.
    """
    changes = []

    def change(section, key, value, reason):
        old = config[section].get(key)
        if old != value:
            config[section][key] = value
            changes.append({'setting': f'{section}.{key}', 'from': old, 'to': value, 'reason': reason})

    change('QEMU', 'Hypervisor', False, 'nested HVF is unavailable')
    change('System', 'MemorySize', min(config['System']['MemorySize'], 4096), 'runner memory')
    for index, network in enumerate(config.get('Network', [])):
        if network.get('Mode') != 'Emulated':
            changes.append({'setting': f'Network[{index}].Mode', 'from': network.get('Mode'),
                            'to': 'Emulated', 'reason': 'nested vmnet is unavailable'})
            network['Mode'] = 'Emulated'
    if config['System'].get('Architecture') == 'aarch64':
        arguments = list(config['QEMU'].get('AdditionalArguments', []))
        if config['System'].get('CPU', 'default') == 'default' and '-cpu' not in arguments:
            change('QEMU', 'AdditionalArguments', arguments + ['-cpu', TCG_AARCH64_CPU],
                   'UTM runs "default" as cortex-a72 without a hypervisor; use the CPU '
                   'Packer installs the image with under TCG')
        # Packer's TCG build uses multi-threaded TCG as well; single-threaded
        # TCG runs all vCPUs on one host thread.
        change('System', 'ForceMulticore', True, 'multi-threaded TCG')
        for index, display in enumerate(config.get('Display', [])):
            # virtio-ramfb[-gl] pairs ramfb with a virtio-gpu device. Under TCG
            # Windows' VirtIO GPU driver (viogpudo.sys from UTM Guest Tools)
            # intermittently bugchecks (0x50) while first-boot device setup
            # starts it. Packer installs and boots the image on plain ramfb.
            if display.get('Hardware') in RAMFB_WITH_VIRTIO_GPU:
                changes.append({'setting': f'Display[{index}].Hardware', 'from': display['Hardware'],
                                'to': 'ramfb', 'reason': "Windows' VirtIO GPU driver bugchecks under TCG"})
                display['Hardware'] = 'ramfb'
        if config['QEMU'].get('TPMDevice'):
            # With a TPM, UTM boots edk2-aarch64-secure-code.fd, which halts in
            # a dead loop under TCG before it reads any disk (UTM 5.0.9). The
            # arm64 image is installed without a TPM (Packer's arm64 VM has
            # none; the installer bypasses the check), so it boots without one.
            change('QEMU', 'TPMDevice', False,
                   'the secure-boot UEFI firmware UTM uses with a TPM halts under TCG')
    return changes


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
            # Only the disposable CI bundle changes.
            result['software_emulation_changes'] = apply_software_emulation(config)
            config_path.write_bytes(plistlib.dumps(config))
        (diagnostics / 'config.plist').write_bytes(config_path.read_bytes())
        # Every failure here fails CI. Select by UUID to avoid touching
        # same-named user VMs.
        try:
            ping_utm()
        except subprocess.TimeoutExpired as error:
            result['utm_state'] = diagnose_utm()
            raise UTMNotResponding(
                f'UTM did not answer the first AppleScript call within {error.timeout:g}s: '
                + '; '.join(result['utm_state']['hints'])) from error
        try:
            register_in_place(bundle, identifier)
        except RegistrationTimeout as error:
            result['stderr'] = error.__cause__.stderr
            result['utm_vms'] = list_utm_vms()
            listed = result['utm_vms']
            if not isinstance(listed, list) or identifier not in {vm['id'] for vm in listed}:
                result['utm_windows'] = utm_windows()
                result['utm_log'] = utm_log()
            raise RegistrationTimeout(f'{error}; see utm_vms and utm_log in result.json') from error
        except RegisteredElsewhere as error:
            raise RegisteredElsewhere(f'Refusing to smoke-test a UUID registered at another '
                                      f'bundle path: {error}') from error
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
