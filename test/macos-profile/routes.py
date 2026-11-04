#!/usr/bin/env python3
"""Select real-Mac checks without combining label triggers with path filters."""
from datetime import datetime, timezone
import fnmatch
import json
import os
import subprocess

MACOS_PATHS = (
    'scripts/macos/**', 'build/packer/linux/ubuntu/linux-ubuntu-on-macos.sh',
    'pkg/build/macos-*.go', 'pkg/build/qemu-arm64.go', 'pkg/deploy/macos-*.go',
    'pkg/deploy/utm*', 'deployments/utm/**', '.github/workflows/test-build-macos.yml',
    'test/macos-profile/golden/**',
)


def select(event_name, event, files, today):
    if event_name == 'workflow_dispatch':
        return False, True, False
    if event_name == 'schedule':
        monthly = event['schedule'] == '0 3 1-7 * *'
        run = today.weekday() == 0 and (monthly == (today.day <= 7))
        return False, run, run and monthly
    pr = event['pull_request']
    labels = {label['name'] for label in pr['labels']}
    release = pr['head']['ref'].startswith('release-please--')
    quick = 'ci:macos' in labels or any(fnmatch.fnmatch(name, pattern) for name in files for pattern in MACOS_PATHS)
    full = release or 'ci:macos-full' in labels or any(name.startswith('.github/runners/') for name in files)
    return quick, full, release


def main():
    with open(os.environ['GITHUB_EVENT_PATH']) as stream:
        event = json.load(stream)
    name = os.environ['GITHUB_EVENT_NAME']
    files = []
    if name == 'pull_request':
        # Paginate instead of using the truncated compare API/path-filter list.
        output = subprocess.check_output(['gh', 'api', '--paginate', f'repos/{os.environ["GITHUB_REPOSITORY"]}/pulls/{event["number"]}/files?per_page=100', '--jq', '.[] | .filename, (.previous_filename // .filename)'], text=True)
        files = output.splitlines()
    result = select(name, event, files, datetime.now(timezone.utc).date())
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        for key, value in zip(('quick', 'full', 'full_amd64'), result):
            print(f'{key}={str(value).lower()}', file=stream)


if __name__ == '__main__':
    main()
