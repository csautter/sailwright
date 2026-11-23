#!/usr/bin/env python3
"""Exercise UTM readiness failures and cleanup without a Mac or a real guest."""
import importlib.util
import json
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('boot_utm', Path(__file__).with_name('boot-utm.py'))
boot_utm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(boot_utm)


class BootTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.bundle = Path(self.temp.name) / 'bundle with spaces.utm'
        self.bundle.mkdir()
        self.diagnostics = Path(self.temp.name) / 'diagnostics'
        self.config = {'Information': {'UUID': 'test-uuid'}, 'QEMU': {'Hypervisor': True},
                       'System': {'MemorySize': 8192}, 'Network': [{'Mode': 'Bridged'}]}
        (self.bundle / 'config.plist').write_bytes(plistlib.dumps(self.config))
        self.running = False
        self.actions = []
        self.addresses = '192.168.64.10'
        self.start_error = None
        self.stop_error = None
        self.open = self.enter(patch.object(boot_utm, 'applescript', return_value=str(self.bundle)))
        self.enter(patch.object(boot_utm, 'vm_command', side_effect=self.command))
        self.enter(patch.object(boot_utm.time, 'sleep'))

    def enter(self, context):
        value = context.start()
        self.addCleanup(context.stop)
        return value

    def command(self, identifier, action):
        self.assertEqual(identifier, 'test-uuid')
        self.actions.append(action)
        if action == 'start targetVM':
            self.running = True
            if self.start_error:
                raise self.start_error
        elif action == 'stop targetVM by force':
            if self.stop_error:
                raise self.stop_error
            self.running = False
        elif action == 'return (status of targetVM) as text':
            return 'started' if self.running else 'stopped'
        elif action == 'return (query ip targetVM)':
            return self.addresses
        return ''

    def run_boot(self, **kwargs):
        boot_utm.boot(self.bundle, 1, self.diagnostics, **kwargs)

    def result(self):
        return json.loads((self.diagnostics / 'result.json').read_text())

    def test_ready_guest_is_stopped_and_deleted(self):
        self.run_boot()
        self.assertEqual(self.result()['addresses'], ['192.168.64.10'])
        self.assertEqual(self.actions[-2:], ['return (status of targetVM) as text', 'delete targetVM'])
        self.assertFalse(self.running)
        self.assertEqual(self.open.call_args_list[0].args[-1], str(self.bundle))

    def test_software_emulation_changes_only_disposable_configuration(self):
        self.run_boot(software_emulation=True)
        config = plistlib.loads((self.bundle / 'config.plist').read_bytes())
        self.assertFalse(config['QEMU']['Hypervisor'])
        self.assertEqual(config['Network'][0]['Mode'], 'Emulated')
        self.assertEqual(config['System']['MemorySize'], 4096)
        self.assertEqual(config['Information']['UUID'], 'test-uuid')

    def test_registration_failure_is_fatal(self):
        self.open.side_effect = subprocess.CalledProcessError(1, ['osascript'], stderr='TCC denied')
        with self.assertRaises(subprocess.CalledProcessError):
            self.run_boot()
        self.assertEqual(self.result()['stderr'], 'TCC denied')
        self.assertNotIn('stop targetVM by force', self.actions)

    def test_copied_uuid_at_another_path_is_not_started_or_deleted(self):
        self.open.return_value = str(self.bundle.parent / 'unrelated.utm')
        with self.assertRaisesRegex(RuntimeError, 'another bundle path'):
            self.run_boot()
        self.assertEqual(self.actions, [])
        self.assertIn('error', self.result())

    def test_missing_bundle_fails_and_retains_diagnostics(self):
        (self.bundle / 'config.plist').unlink()
        with self.assertRaises(FileNotFoundError):
            self.run_boot()
        self.assertIn('error', self.result())
        self.open.assert_not_called()

    def test_guest_agent_unavailable_is_retried(self):
        normal = self.command
        failures = [True]

        def command(identifier, action):
            if action == 'return (query ip targetVM)' and failures:
                failures.pop()
                raise subprocess.CalledProcessError(1, ['osascript'], stderr='agent not ready')
            return normal(identifier, action)

        with patch.object(boot_utm, 'vm_command', side_effect=command):
            self.run_boot()
        self.assertEqual(failures, [])
        self.assertIn('addresses', self.result())

    def status_sequence(self, statuses):
        normal = self.command

        def command(identifier, action):
            if action == 'return (status of targetVM) as text' and self.running and statuses:
                return statuses.pop(0)
            return normal(identifier, action)

        return patch.object(boot_utm, 'vm_command', side_effect=command)

    def test_stale_stopped_status_after_start_is_tolerated(self):
        statuses = ['stopped', 'stopped', 'starting']
        with self.status_sequence(statuses):
            self.run_boot()
        self.assertEqual(statuses, [])
        self.assertIn('addresses', self.result())
        self.assertIn('delete targetVM', self.actions)

    def test_vm_that_never_leaves_stopped_fails_after_grace(self):
        clock = iter([0, 0, 0, 10, boot_utm.START_GRACE_SECONDS, boot_utm.START_GRACE_SECONDS])
        self.enter(patch.object(boot_utm.time, 'monotonic', side_effect=lambda: next(clock, 10**6)))
        with self.status_sequence(['stopped'] * 4):
            with self.assertRaisesRegex(RuntimeError, 'stopped before the guest became ready'):
                boot_utm.boot(self.bundle, 1000, self.diagnostics)
        self.assertIn('delete targetVM', self.actions)

    def test_vm_that_stops_after_running_fails_immediately(self):
        with self.status_sequence(['starting', 'stopped']):
            with self.assertRaisesRegex(RuntimeError, 'stopped before the guest became ready'):
                boot_utm.boot(self.bundle, 1000, self.diagnostics)

    def test_already_running_guest_is_not_touched(self):
        self.running = True
        with self.assertRaisesRegex(RuntimeError, 'already running'):
            self.run_boot()
        self.assertNotIn('start targetVM', self.actions)
        self.assertNotIn('stop targetVM by force', self.actions)

    def test_partial_start_failure_still_cleans_up(self):
        self.start_error = subprocess.TimeoutExpired(['osascript'], 60)
        with self.assertRaises(subprocess.TimeoutExpired):
            self.run_boot()
        self.assertIn('delete targetVM', self.actions)
        self.assertIn('error', self.result())

    def test_no_ready_guest_times_out_and_cleans_up(self):
        self.addresses = '127.0.0.1, fe80::1'
        self.enter(patch.object(boot_utm.time, 'monotonic', side_effect=[0, 0, 2, 3]))
        with self.assertRaises(TimeoutError):
            self.run_boot()
        self.assertIn('return (query ip targetVM)', self.actions)
        self.assertIn('delete targetVM', self.actions)

    def test_cleanup_failure_is_fatal_and_recorded(self):
        self.stop_error = RuntimeError('stop denied')
        with self.assertRaisesRegex(RuntimeError, 'stop denied'):
            self.run_boot()
        self.assertEqual(self.result()['cleanup_error'], 'stop denied')
        self.assertNotIn('delete targetVM', self.actions)

    def test_only_usable_guest_addresses_count_as_ready(self):
        self.assertEqual(boot_utm.guest_addresses('127.0.0.1, ::1, 169.254.1.2, fe80::1, 0.0.0.0, invalid'), [])
        self.assertEqual(boot_utm.guest_addresses('192.168.1.2, fd00::2'), ['192.168.1.2', 'fd00::2'])


    def test_hanging_first_call_reports_utm_state(self):
        self.open.side_effect = subprocess.TimeoutExpired(['osascript'], 60)
        state = {'processes': [{'pid': 7, 'state': 'T', 'path': '/Applications/UTM.app/Contents/MacOS/UTM'}],
                 'hints': ['UTM is suspended (process state T)', 'no Automation grant']}
        with patch.object(boot_utm, 'diagnose_utm', return_value=state):
            with self.assertRaisesRegex(boot_utm.UTMNotResponding,
                                        r'within 60s: UTM is suspended \(process state T\); no Automation grant'):
                self.run_boot()
        self.assertEqual(self.result()['utm_state'], state)
        self.assertEqual(self.result()['status'], 'not started')
        self.assertEqual(self.actions, [])

    def test_later_timeouts_also_record_utm_state(self):
        self.start_error = subprocess.TimeoutExpired(['osascript'], 60)
        with patch.object(boot_utm, 'diagnose_utm', return_value={'hints': ['x']}) as diagnose:
            with self.assertRaises(subprocess.TimeoutExpired):
                self.run_boot()
        diagnose.assert_called_once()
        self.assertEqual(self.result()['utm_state'], {'hints': ['x']})


class DiagnoseTest(unittest.TestCase):
    """Probe parsing for a UTM that ignores Apple Events (no Mac required)."""

    def fake_run(self, outputs):
        calls = []

        def run(command, **kwargs):
            calls.append(command)
            self.assertEqual(kwargs['timeout'], boot_utm.PROBE_TIMEOUT_SECONDS)
            for key, value in outputs.items():
                if command[0] == key:
                    if isinstance(value, Exception):
                        raise value
                    code, out = value
                    return subprocess.CompletedProcess(command, code, out, 'failed' if code else '')
            raise AssertionError(f'unexpected probe {command}')

        return patch.object(boot_utm.subprocess, 'run', side_effect=run), calls

    def test_gatekeeper_and_missing_grant_are_named(self):
        context, calls = self.fake_run({
            'ps': (0, '  1 Ss /sbin/launchd\n 881 T  /Applications/UTM.app/Contents/MacOS/UTM\n'),
            'xattr': (0, '0181;6b033a46;Homebrew Cask;\n'),
            'osascript': (0, 'Dock\t20\nCoreServicesUIAgent\t8\nUserNotificationCenter\t8\nFinder\t0\n'),
            'sqlite3': (0, ''),
        })
        with context:
            state = boot_utm.diagnose_utm()
        self.assertEqual(state['processes'], [{'pid': 881, 'state': 'T',
                                               'path': '/Applications/UTM.app/Contents/MacOS/UTM'}])
        self.assertEqual(state['quarantine'], '0181;6b033a46;Homebrew Cask;')
        self.assertEqual([w['owner'] for w in state['prompt_windows']],
                         ['CoreServicesUIAgent', 'UserNotificationCenter'])
        self.assertEqual(state['automation_grants'], [])
        hints = ' | '.join(state['hints'])
        for expected in ('process state T', 'com.apple.quarantine', 'Gatekeeper first-launch',
                         'privacy consent', 'no Automation'):
            self.assertIn(expected, hints)
        self.assertNotIn('tell application "UTM"', repr(calls))
        self.assertIn('-readonly', calls[-1])

    def test_prepared_utm_reports_no_known_blocker(self):
        context, _ = self.fake_run({
            'ps': (0, ' 42 S  /Applications/UTM.app/Contents/MacOS/UTM\n'),
            'xattr': (1, ''),
            'osascript': (0, 'Finder\t0\n'),
            'sqlite3': (0, '/usr/libexec/sshd-keygen-wrapper\t2\n'),
        })
        with context:
            state = boot_utm.diagnose_utm()
        self.assertIsNone(state['quarantine'])
        self.assertEqual(state['automation_grants'], [{'client': '/usr/libexec/sshd-keygen-wrapper',
                                                       'auth_value': 2}])
        self.assertEqual(state['hints'], ['no known blocker found; UTM may still be starting'])

    def test_denied_grant_and_stopped_utm(self):
        context, _ = self.fake_run({
            'ps': (0, ''), 'xattr': (1, ''), 'osascript': (0, ''),
            'sqlite3': (0, '/usr/libexec/sshd-keygen-wrapper\t0\n'),
        })
        with context:
            state = boot_utm.diagnose_utm()
        self.assertIn('UTM is not running', state['hints'])
        self.assertIn('Automation (Apple Events) for UTM is denied in the user TCC database', state['hints'])

    def test_failing_probes_are_reported_not_raised(self):
        context, _ = self.fake_run({
            'ps': subprocess.TimeoutExpired(['ps'], 10), 'xattr': FileNotFoundError('xattr'),
            'osascript': (1, ''), 'sqlite3': (14, ''),
        })
        with context:
            state = boot_utm.diagnose_utm()
        for key in ('processes', 'quarantine', 'prompt_windows', 'automation_grants'):
            self.assertTrue(state[key].startswith('unknown: '), (key, state[key]))
        self.assertEqual(state['hints'], ['no known blocker found; UTM may still be starting'])


if __name__ == '__main__':
    unittest.main()
