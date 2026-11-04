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
        self.open = self.enter(patch.object(boot_utm, 'applescript'))
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
        self.assertEqual(self.open.call_args.args[-1], str(self.bundle))

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


if __name__ == '__main__':
    unittest.main()
