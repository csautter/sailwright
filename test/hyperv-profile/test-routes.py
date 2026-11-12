#!/usr/bin/env python3
import unittest
from routes import select


class Routing(unittest.TestCase):
    def pr(self, files=(), labels=(), branch='fix/example', added=None, actor='csautter'):
        event = {'pull_request': {'labels': [{'name': label} for label in labels], 'head': {'ref': branch}}}
        if added:
            event.update(action='labeled', label={'name': added})
        return select('pull_request', event, files, actor)

    def test_routed_paths_select_hyperv(self):
        for path in ('go.mod', 'pkg/build/windows-hyperv.go', 'scripts/windows/install.ps1', 'test/hyperv-profile/routes.py'):
            self.assertTrue(self.pr([path]), path)

    def test_other_platform_and_unrouted_paths_do_not(self):
        for path in ('README.md', 'pkg/build/linux-build.go', 'pkg/deploy/utm-deploy.go'):
            self.assertFalse(self.pr([path]), path)

    def test_label_and_release_branch_select_hyperv(self):
        self.assertTrue(self.pr(['README.md'], ['ci:hyperv']))
        self.assertTrue(self.pr(branch='release-please--branches--main'))

    def test_unrelated_label_events_start_nothing(self):
        self.assertFalse(self.pr(['go.mod'], ['ci:hyperv', 'dependencies'], added='dependencies'))

    def test_dependabot_runs_skip_hyperv_because_they_get_no_secrets(self):
        self.assertFalse(self.pr(['go.mod', 'go.sum'], branch='dependabot/go_modules/x', actor='dependabot[bot]'))
        self.assertFalse(self.pr(['go.mod'], ['ci:hyperv'], actor='dependabot[bot]'))

    def test_maintainer_label_on_dependabot_pr_still_runs(self):
        self.assertTrue(self.pr(['go.mod'], ['ci:hyperv'], branch='dependabot/go_modules/x', added='ci:hyperv'))

    def test_dispatch_and_schedule_ignore_actor(self):
        self.assertTrue(select('workflow_dispatch', {}, [], 'dependabot[bot]'))
        self.assertTrue(select('schedule', {}, [], ''))
        self.assertFalse(select('push', {}, [], ''))


if __name__ == '__main__':
    unittest.main()
