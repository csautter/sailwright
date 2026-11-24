#!/usr/bin/env python3
"""Exercise test-utm.py --register without a Mac that can drive UTM."""
import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('test_utm', Path(__file__).with_name('test-utm.py'))
test_utm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(test_utm)
boot_utm = test_utm.boot_utm


class UTMAnswersTest(unittest.TestCase):
    def test_timeout_means_unavailable(self):
        with patch.object(boot_utm, 'applescript', side_effect=subprocess.TimeoutExpired(['osascript'], 60)):
            with self.assertRaisesRegex(test_utm.UTMUnavailable, 'within 60s'):
                test_utm.utm_answers()

    def test_apple_event_error_means_unavailable(self):
        error = subprocess.CalledProcessError(1, ['osascript'], stderr='Not authorized (-1743)\n')
        with patch.object(boot_utm, 'applescript', side_effect=error):
            with self.assertRaisesRegex(test_utm.UTMUnavailable, r'Not authorized \(-1743\)'):
                test_utm.utm_answers()

    def test_answer_is_read_only_and_shared_with_boot(self):
        with patch.object(boot_utm, 'applescript', return_value='') as script:
            test_utm.utm_answers()
        self.assertEqual(script.call_args.args, (boot_utm.PING_SCRIPT,))
        self.assertEqual(boot_utm.PING_SCRIPT, ['tell application "UTM" to get id of virtual machines'])


class VerifyRegistrationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.bundle = Path(self.temp.name) / 'fixture.utm'
        self.bundle.mkdir()
        for name, kwargs in (('register_bundle', {}), ('vm_command', {}),
                             ('list_utm_vms', {'return_value': []})):
            context = patch.object(boot_utm, name, **kwargs)
            setattr(self, name, context.start())
            self.addCleanup(context.stop)

    def lookups(self, *results):
        """registered_path answers: wait_for_registration first, then the cleanup lookup."""
        return patch.object(boot_utm, 'registered_path', side_effect=list(results))

    def unknown(self):
        return subprocess.CalledProcessError(1, ['osascript'], stderr="Can't get virtual machine id (-1728)")

    def verify(self):
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            test_utm.verify_registration(self.bundle, 'uuid', 'ubuntu/arm64')
        return err.getvalue()

    def test_listed_at_bundle_path_passes_and_unregisters(self):
        listed = str(self.bundle) + '/'
        with self.lookups(listed, listed):
            self.assertEqual(self.verify(), '')
        self.register_bundle.assert_called_once_with(self.bundle)
        self.vm_command.assert_called_once_with('uuid', 'delete targetVM')

    def test_uuid_never_listed_fails_loudly(self):
        timeout = boot_utm.RegistrationTimeout('UTM did not list virtual machine id uuid within 60s after open')
        self.list_utm_vms.return_value = [{'id': 'other', 'name': 'x', 'path': None}]
        with patch.object(boot_utm, 'wait_for_registration', side_effect=timeout), \
                self.lookups(self.unknown()):
            with self.assertRaisesRegex(AssertionError, r"FAILED for ubuntu/arm64: .*within 60s after open; "
                                                        r"UTM lists: \[\{'id': 'other'"):
                self.verify()
        self.vm_command.assert_not_called()

    def test_late_registration_after_timeout_is_still_cleaned_up(self):
        timeout = boot_utm.RegistrationTimeout('not listed')
        with patch.object(boot_utm, 'wait_for_registration', side_effect=timeout), \
                self.lookups(str(self.bundle)):
            with self.assertRaises(AssertionError):
                self.verify()
        self.vm_command.assert_called_once_with('uuid', 'delete targetVM')

    def test_uuid_at_another_path_fails_and_is_not_deleted(self):
        with self.lookups('/elsewhere/fixture.utm', '/elsewhere/fixture.utm'):
            with self.assertRaisesRegex(AssertionError, 'FAILED for ubuntu/arm64: .*expected'):
                self.verify()
        self.vm_command.assert_not_called()

    def test_failed_cleanup_warns_without_hiding_the_result(self):
        self.vm_command.side_effect = subprocess.CalledProcessError(1, ['osascript'], stderr='busy')
        with self.lookups(str(self.bundle), str(self.bundle)):
            self.assertIn('WARNING: could not unregister ubuntu/arm64', self.verify())

    def test_launchservices_failure_fails_and_needs_no_cleanup(self):
        self.register_bundle.side_effect = subprocess.CalledProcessError(1, ['open'])
        with self.lookups(self.unknown()):
            with self.assertRaises(subprocess.CalledProcessError):
                self.verify()
        self.vm_command.assert_not_called()


class MainRegisterTest(unittest.TestCase):
    """Full bundle generation (needs qemu-img and envsubst) with UTM stubbed."""

    def run_main(self):
        out = io.StringIO()
        with patch.object(sys, 'argv', ['test-utm.py', '--register']), contextlib.redirect_stdout(out):
            test_utm.main()
        return out.getvalue()

    def test_unavailable_utm_is_reported_as_skipped_once_never_passed(self):
        with patch.object(test_utm, 'utm_answers', side_effect=test_utm.UTMUnavailable('no events')) as answers, \
                patch.object(test_utm, 'verify_registration') as verify:
            output = self.run_main()
        answers.assert_called_once()
        verify.assert_not_called()
        self.assertEqual(output.count('UTM registration SKIPPED for all bundles: no events'), 1)
        # Pinged once, before any bundle: the SKIPPED line comes first.
        self.assertTrue(output.startswith('UTM registration SKIPPED'), output)
        self.assertNotIn('registration passed', output)
        self.assertEqual(output.count('OK: UTM '), 6)

    def test_every_bundle_is_verified_when_utm_answers(self):
        with patch.object(test_utm, 'utm_answers'), \
                patch.object(test_utm, 'verify_registration') as verify:
            output = self.run_main()
        self.assertEqual(verify.call_count, 6)
        for bundle, identifier, label in (call.args for call in verify.call_args_list):
            self.assertTrue(bundle.name.endswith('-dev-alchemy.utm'), bundle)
            self.assertRegex(identifier, r'^[0-9A-F]{8}-')
            self.assertTrue(bundle.name.startswith(label.replace('/', '-')), (bundle, label))
        self.assertEqual(output.count('listed at its bundle path'), 6)

    def test_registration_failure_fails_the_run(self):
        with patch.object(test_utm, 'utm_answers'), \
                patch.object(test_utm, 'verify_registration', side_effect=AssertionError('not listed')):
            with self.assertRaisesRegex(AssertionError, 'not listed'):
                self.run_main()


if __name__ == '__main__':
    unittest.main()
