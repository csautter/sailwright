"""Route Azure Hyper-V builds using complete, paginated PR file lists."""
import fnmatch
import json
import os
import subprocess

PATHS = (
    'build/packer/windows/hyperv/**',
    'build/packer/windows/windows11-on-windows-hyperv.pkr.hcl',
    # Shared build and deploy code is routed unless another platform owns it,
    # so new shared helpers trigger the build by default.
    'pkg/build/**', 'pkg/deploy/**', 'internal/winpsenv/**',
    'cmd/main.go', 'cmd/cmd/install*.go',
    'runtimeassets.go', 'go.mod', 'go.sum', 'Makefile',
    'scripts/windows/**', 'scripts/common/**', 'scripts/gh-runner-func/**',
    '.github/actions/download-build-cache/**', '.github/actions/upload-build-cache/**',
    '.github/workflows/test-build-windows.yml', 'test/hyperv-profile/**',
)

# Files in routed packages that the Windows Hyper-V build never compiles or runs.
OTHER_PLATFORMS = (
    'pkg/build/linux*', 'pkg/build/macos*', 'pkg/build/qemu*', 'pkg/build/utm*',
    'pkg/build/windows-linux-*', 'pkg/build/*_unix*.go', 'pkg/build/*_linux*.go',
    'pkg/build/*_other*.go',
    'pkg/deploy/linux*', 'pkg/deploy/macos*', 'pkg/deploy/tart-*', 'pkg/deploy/utm*',
)


def routed(path):
    return (any(fnmatch.fnmatchcase(path, pattern) for pattern in PATHS)
            and not any(fnmatch.fnmatchcase(path, pattern) for pattern in OTHER_PLATFORMS))


# GitHub gives runs that Dependabot triggers no Actions secrets, so the Azure
# login cannot succeed. A maintainer who adds ci:hyperv starts a run as
# themselves, which does get the secrets.
SECRETLESS_ACTORS = ('dependabot[bot]',)


def select(name, event, files, actor=''):
    if name != 'pull_request':
        return name in ('workflow_dispatch', 'schedule')
    if event.get('action') == 'labeled' and event['label']['name'] != 'ci:hyperv':
        return False
    if actor in SECRETLESS_ACTORS:
        return False
    pr = event['pull_request']
    return (pr['head']['ref'].startswith('release-please--')
            or any(label['name'] == 'ci:hyperv' for label in pr['labels'])
            or any(routed(path) for path in files))


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
    selected = select(name, event, files, os.environ.get('GITHUB_ACTOR', ''))
    matched = next((path for path in files if routed(path)), None)
    print(f'event={name} actor={os.environ.get("GITHUB_ACTOR", "-")} '
          f'hyperv={str(selected).lower()} first_routed_path={matched or "-"}')
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        print(f'hyperv={str(selected).lower()}', file=stream)
