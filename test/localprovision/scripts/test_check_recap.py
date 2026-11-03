import unittest

from check_recap import validate


class RecapTest(unittest.TestCase):
    def test_colored_prefixed_output(self):
        output = "[local:linux:provision][stdout] PLAY RECAP ****\n"
        output += "\x1b[32m[stdout] localhost : ok=3 changed=0 unreachable=0 failed=0 skipped=1 rescued=0 ignored=0\x1b[0m"
        validate(output, "idempotency")

    def test_reject_missing_or_failed_recap(self):
        for output in ["", "PLAY RECAP", "PLAY RECAP\nhost : ok=1 changed=0 unreachable=1 failed=0", "PLAY RECAP\nhost : ok=1 changed=0 unreachable=0 failed=1"]:
            with self.subTest(output=output), self.assertRaises(ValueError):
                validate(output, "check")

    def test_check_and_apply_allow_changes(self):
        for phase in ["check", "apply"]:
            validate("PLAY RECAP\nhost : ok=1 changed=2 unreachable=0 failed=0", phase)

    def test_idempotency_reports_changed_task(self):
        output = "TASK [role : Install tool] ****\nchanged: [localhost]\nPLAY RECAP\nhost : ok=1 changed=1 unreachable=0 failed=0"
        with self.assertRaisesRegex(ValueError, "Install tool"):
            validate(output, "idempotency")

    def test_every_host_is_checked(self):
        output = "PLAY RECAP\na : ok=1 changed=0 unreachable=0 failed=0\nb : ok=1 changed=1 unreachable=0 failed=0"
        with self.assertRaises(ValueError):
            validate(output, "idempotency")
