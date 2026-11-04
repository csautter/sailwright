#!/usr/bin/env python3
from datetime import date
import unittest
from routes import select


class Routing(unittest.TestCase):
    def pr(self, files=(), labels=(), branch='fix/example'):
        event = {'pull_request': {'labels': [{'name': label} for label in labels], 'head': {'ref': branch}}}
        return select('pull_request', event, files, date(2026, 11, 4))

    def test_shared_and_other_platform_changes_only_use_linux_profile(self):
        for path in ('pkg/build/linux-build.go', 'scripts/linux/example.sh', 'scripts/windows/install.ps1', 'build/packer/windows/windows11-qemu.pkr.hcl'):
            self.assertEqual(self.pr([path]), (False, False, False), path)

    def test_macos_paths_and_goldens_run_quick(self):
        for path in ('scripts/macos/install.sh', 'pkg/deploy/macos-silicon-deploy.go', 'test/macos-profile/golden/windows11-arm64.json'):
            self.assertEqual(self.pr([path]), (True, False, False), path)

    def test_labels_override_paths(self):
        self.assertEqual(self.pr(['README.md'], ['ci:macos']), (True, False, False))
        self.assertEqual(self.pr(['README.md'], ['ci:macos-full']), (False, True, False))

    def test_release_and_runner_changes(self):
        self.assertEqual(self.pr(branch='release-please--branches--main'), (False, True, True))
        self.assertEqual(self.pr(['.github/runners/image.sh']), (False, True, False))

    def test_dispatch(self):
        self.assertEqual(select('workflow_dispatch', {}, [], date(2026, 11, 4)), (False, True, False))

    def test_monthly_and_weekly_do_not_duplicate(self):
        for day in range(1, 31):
            today = date(2026, 11, day)
            weekly = select('schedule', {'schedule': '0 2 * * 1'}, [], today)
            monthly = select('schedule', {'schedule': '0 3 1-7 * *'}, [], today)
            self.assertEqual(weekly[1] or monthly[1], today.weekday() == 0)
            self.assertFalse(weekly[1] and monthly[1])
            self.assertEqual(monthly[2], today.weekday() == 0 and day <= 7)


if __name__ == '__main__':
    unittest.main()
