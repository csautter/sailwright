# Release test kit

Scripted host checks for a release candidate on macOS, Linux and Windows. They
complement the [deterministic E2E scenarios](../../docs/testing-workflows.md#deterministic-e2e-scenarios):
scenarios cover repeatable VM workflows, while this kit adds what a scenario cannot
express, such as Ctrl+C in a real terminal, two concurrent CLI processes, gateway
restarts with different approval modes, and evidence from hypervisor tools.

The kit is built for supervised runs. Each **phase** is one command that a human
reviews and approves once, instead of approving every single command:

```bash
cp test/release/release.env.example test/release/release.env   # adjust
bash test/release/unix.sh preflight kit cli                    # macOS / Linux
```

```powershell
Copy-Item test\release\release.env.ps1.example test\release\release.env.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File test\release\windows.ps1 preflight kit cli
```

See the [release testing runbook](../../docs/release-testing.md) for the working
protocol, where each kind of check must run, and known pitfalls.

## Phases

| Phase | Changes | Needs |
| --- | --- | --- |
| `preflight` | nothing | records host, tools, commit, existing VMs |
| `kit` | `dist/` | Go; builds the CLI and gateway with a local version, runs unit, race and fixture tests |
| `cli` | temp dirs only | read-only CLI and gateway surface, update settings, config validation |
| `selfupdate` | temp dirs only | network; `RT_RELEASE_TAG` / `RT_UPDATE_FROM` for released versions |
| `install` | **host** | disposable VM (`RT_DISPOSABLE=1`); installers and `sailwright install` |
| `provision-local` | **host** | disposable VM; full bundled setup: check, apply, idempotent re-run |
| `provision-interactive` | **host** | run by the human in a terminal: sudo password prompts (macOS/Linux) |
| `hyperv-verify` | nothing | Windows, after `install --hyperv` and a restart |
| `netclass` | **libvirt network** | disposable Linux VM with libvirtd; IPv6 NAT classification, restores the network |
| `interrupt` | run dir | Packer and QEMU; real Ctrl+C through a pseudo-terminal (macOS/Linux) |
| `build` | run dir | hypervisor; full headless build, markers, interrupted OCI pull |
| `vm` | VMs it creates | hypervisor; E2E scenarios, identity, lock contention, network mode |
| `gateway` | VMs it creates | hypervisor, `sshpass` (macOS/Linux); a private gateway on port `RT_GATEWAY_PORT` |
| `cleanup` | nothing | verifies that no VM or process of the run is left |
| `report` | run dir | combines the phase summaries into `report.md` |

By default the build, VM and gateway phases use an isolated app-data directory
under the run directory (`DEV_ALCHEMY_APP_DATA_DIR`), so cached images and VM
bundles of the operator are not touched. Hypervisor VM names are still global:
every phase that creates a VM first checks that the target does not exist.
`RT_APPDATA=default` uses the normal directories instead.

Invocation `RT_*` settings override `release.env`, including explicit empty values.
Use a separate `RT_RUN_DIR` for a second target and clear target-specific pull and
guard settings when switching hypervisors.

## Results

Each phase writes `test/release/out/<host>-<date>/<phase>/`:

- `summary.md`: one row per check with `PASS`, `FAIL`, `SKIP` or `MANUAL`;
- `results.tsv`: the same as tab-separated values;
- `logs/<ID>.log`: command, exit status and redacted output of each check;
- `manual.txt`: steps that only a human can perform or judge;
- `e2e-<ID>/`: reports of E2E scenarios run by the phase.

Guest passwords are redacted before anything is written, and checks refuse
commands with `--show-password`. Check IDs match the release test plans.

## Writing checks

`lib/rt.sh` (Bash 3.2+) and `lib/rt.ps1` (PowerShell 5.1+) provide the same
helpers: a check has an ID, a description, the command and its expectations
(exit status, regexes, a JSON predicate, a timeout). `lib/ptyctl.py` runs a
command in a pseudo-terminal and presses Ctrl+C at defined points:

```bash
python3 test/release/lib/ptyctl.py --log build.log \
  --step 'wait:(?i)downloading:600' --step intr --step exit:30 -- \
  sailwright build ubuntu --arch arm64 --headless
```

The PTY helper keeps its JSON summary on stdout and writes the terminal transcript
to stderr as well as `--log`, so output assertions and JSON checks can be combined.
It provides a 24-row, 120-column terminal so progress renderers behave normally.
Run the offline Unix helper regressions with
`python3 -I -m unittest discover -s test/release/tests -v` (also run by `kit` and CI).

Prefer a YAML scenario under `test/e2e/scenarios` when a check fits its step
model; add a phase check only for what scenarios cannot express.
