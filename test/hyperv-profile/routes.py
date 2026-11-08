"""Route Azure Hyper-V builds using complete, paginated PR file lists."""
import fnmatch
import json
import os
import subprocess

PATHS = (
    'build/packer/windows/hyperv/**',
    'build/packer/windows/windows11-on-windows-hyperv.pkr.hcl',
    'pkg/build/windows-build*.go', 'pkg/build/dependencies*.go',
    'pkg/build/directories*.go', 'pkg/build/generic_build*.go',
    'pkg/build/win11iso*.go', 'pkg/build/hyperv*.go',
    'pkg/build/helper*.go', 'pkg/build/virtual-machine*.go',
    'pkg/build/artifact_state*.go', 'pkg/build/embedded_project*.go',
    'pkg/build/process*.go', 'pkg/build/external_process*.go', 'internal/winpsenv/**',
    'pkg/deploy/windows-hyperv*.go',
    'runtimeassets.go', 'go.mod', 'go.sum', 'Makefile',
    'scripts/windows/**', 'scripts/common/**', 'scripts/gh-runner-func/**',
    '.github/actions/download-build-cache/**', '.github/actions/upload-build-cache/**',
    '.github/workflows/test-build-windows.yml', 'test/hyperv-profile/**',
)


def select(name, event, files):
    if name != 'pull_request':
        return name in ('workflow_dispatch', 'schedule')
    if event.get('action') == 'labeled' and event['label']['name'] != 'ci:hyperv':
        return False
    pr = event['pull_request']
    return (pr['head']['ref'].startswith('release-please--')
            or any(label['name'] == 'ci:hyperv' for label in pr['labels'])
            or any(fnmatch.fnmatchcase(path, pattern) for path in files for pattern in PATHS))


if __name__ == '__main__':
    with open(os.environ['GITHUB_EVENT_PATH']) as stream:
        event = json.load(stream)
    name = os.environ['GITHUB_EVENT_NAME']
    files = []
    if name == 'pull_request' and not (event.get('action') == 'labeled' and event['label']['name'] != 'ci:hyperv'):
        files = subprocess.check_output([
            'gh', 'api', '--paginate',
            f'repos/{os.environ["GITHUB_REPOSITORY"]}/pulls/{event["number"]}/files?per_page=100',
            '--jq', '.[] | .filename, (.previous_filename // .filename)',
        ], text=True).splitlines()
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        print(f'hyperv={str(select(name, event, files)).lower()}', file=stream)
