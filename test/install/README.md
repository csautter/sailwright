# Installer validation

The offline tests exercise replacement recovery and Windows start diagnostics:

```bash
python3 -m unittest discover -s test/install -v
```

In PowerShell 7 or Windows PowerShell 5.1 with Pester 5.5 or newer:

```powershell
Import-Module Pester -MinimumVersion 5.5.0
Invoke-Pester -Path test/install/install.Tests.ps1 -Output Detailed -CI
```

The `Test Install Scripts` workflow runs these checks and real release installs
on Linux, macOS and both Windows shells. Mocked process failures verify the
policy message; they do not verify an actual AppLocker/WDAC configuration.

## Manual Windows VM acceptance test

**Pending:** execute this procedure on a fresh disposable Windows VM before
marking the installation/removal documentation as verified. CI's release
installation checks do not exercise removal of all host dependencies.

Use Windows 11 Pro/Enterprise or Windows Server with an administrator account,
network access to the documented package sources, enough disk for the packages,
and a VM snapshot. Nested virtualization is needed only for the optional
Hyper-V test. Keep host dependency changes inside the disposable VM.

1. Check out the PR branch in the VM. Record the commit SHA, Windows edition
   and build, shell version, architecture and pre-existing software. Snapshot
   the VM. Save user and machine PATH as raw registry values (including value
   types), the installed-app list, `choco list` if available, existing Cygwin
   root/data, services, and optional-feature state. Record any tooling added
   just to run these tests so it is not mistaken for installer output.
2. From a normal, non-elevated shell, run the **checkout's** installer:

   ```powershell
   & .\scripts\install.ps1
   ```

   It downloads the current release CLI but uses this PR's installer code.
   Expect the installed path, version output and the next-step hint, without
   UAC. Verify both binaries, the user PATH entry, unchanged machine PATH and
   no newly registered services. Repeat using a custom path containing spaces
   and with `-NoGateway` after reverting the snapshot. Test the piped entry
   point with `Get-Content -Raw .\scripts\install.ps1 | Invoke-Expression` too.
3. On another snapshot, add a temporary user PATH entry containing an
   unexpanded reference such as `%USERPROFILE%\installer-test-bin`. Rerun the
   installer and confirm this reference remains literal in the raw registry
   value, the install directory is not duplicated, and a fresh terminal sees it.
4. Run the offline Pester suite above in **both** Windows PowerShell 5.1 and
   PowerShell 7. Preserve the results.
5. On a separate disposable snapshot configured by the Windows administrator
   for enforced application control, deny execution of the installed
   `sailwright.exe` while allowing the shell and installer. Rerun the checkout's
   installer. For Win32 error 1260 expect “installed … Windows policy blocks
   running it”, an IT approval hint and a documentation link, with no next-step
   success hint. Confirm files and PATH remain present. Other start failures
   must show “version check failed” and the underlying detail. Record the
   actual native error and policy type, especially for WDAC, whose errors can
   differ. Do not change company policy to conduct this test.
6. Revert to the unrestricted snapshot. From the PR checkout, use a CLI built
   from that commit (with Go available: `go build -o sailwright-review.exe
   ./cmd/main.go`). Run:

   ```powershell
   .\sailwright-review.exe install --hyperv=false --vagrant=false
   ```

   Accept UAC. Compare packages, Cygwin root, pip packages and machine PATH
   with [What gets installed](../../docs/what-gets-installed.md#windows-host-dependencies).
   Reopen the shell before checking tool availability. Preserve the install
   output, including any package failure; a failed dependency install is not
   a completed acceptance test.
7. Follow that page's **Remove Windows dependencies** and **Remove the CLI**
   steps. Compare with the baseline: executables removed, user and machine
   PATH entries removed/restored without disturbing other entries, Cygwin
   leftovers accounted for, and Chocolatey retained or removed as documented.
   Check a new terminal with `Get-Command sailwright -ErrorAction SilentlyContinue`.
   Pre-existing tools and data must remain intact. Record remaining vendor
   caches/logs and whether manual cleanup was required.
8. Use separate snapshots for `--with-go`, `--virtualbox`, `--vagrant` and
   `--hyperv` if validating those paths. For Hyper-V, record restart behavior,
   ADK removal and feature/role removal after the test. Do not claim an
   unexecuted optional path as passed.

Attach a short result to the PR: commit, environment, commands, pass/fail for
CLI install, raw PATH preservation, enforced-policy diagnostic and dependency
removal, plus exceptions or unresolved leftovers. Keep the PR in draft until
the selected Windows acceptance checks have been reviewed.
