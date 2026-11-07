# Troubleshooting

This page collects rare, host-specific issues that do not affect most Sailwright setups.

## Managed / company machines

Windows application control (AppLocker, App Control for Business / WDAC or
Software Restriction Policies) can prevent an executable from starting,
including an unsigned binary in Downloads or another user-writable directory.
The default `%LOCALAPPDATA%\Programs\sailwright` directory is also user-writable.
A successful download and checksum check does not imply permission to execute.

If `install.ps1` reports that Sailwright is installed but Windows policy blocks
running it, the files and user PATH entry remain installed; host dependency
setup has not run. Other failed version checks include the underlying error
because architecture, permissions or signature validation can also prevent a
launch. A policy that blocks PowerShell or the installer itself prevents the
script from producing this diagnostic.

Give IT the executable path, release version, SHA-256, the complete error and
the time of the failed launch. Ask them to check application-control event
logs and approve an installation location and executable under their policy.
[AppLocker supports publisher, path and file-hash rules](https://learn.microsoft.com/en-us/windows/security/application-security/application-control/app-control-for-business/applocker/working-with-applocker-rules).
Publisher rules require a suitable signed binary; checksum/cosign verification
of release downloads is separate from Windows Authenticode signing. IT can
assess the appropriate rule and software distribution method. An administrator
shell or PowerShell execution-policy change does not establish approval for a
blocked executable.

Share [What gets installed](./what-gets-installed.md) before requesting approval:
`sailwright install` needs elevated rights and installs additional software.
WSL2 may be an alternative if your organization approves it; a complete
Sailwright-on-WSL2 setup guide is not available yet. Coordinate with IT before
choosing that route.

## Windows: Cygwin Ansible Shadowed by a Host Installation

On Windows with Cygwin, the Ansible installation inside Cygwin can be shadowed by another Python or Ansible installation from the Windows host.

If that happens:

- Do not install Ansible directly on the Windows host.
- Remove conflicting host-level Ansible installations.
- Install and run Ansible through the Cygwin Python environment instead.

## macOS: Ansible Fork Safety Crash

Running Ansible on macOS can trigger a fork-safety crash similar to this:

```bash
TASK [Gathering Facts] ***************************************************************************************************************************************
objc[9473]: +[NSNumber initialize] may have been in progress in another thread when fork() was called.
objc[9473]: +[NSNumber initialize] may have been in progress in another thread when fork() was called. We cannot safely call it or ignore it in the fork() child process. Crashing instead. Set a breakpoint on objc_initializeAfterForkError to debug.
ERROR! A worker was found in a dead state
```

Work around it by setting this environment variable before running Ansible:

```bash
export OBJC_DISABLE_INITIALIZE_FORK_SAFETY=YES
```

## Linux: `virsh domifaddr` Returns No IP

Linux libvirt provisioning waits for `virsh domifaddr` to report a guest IPv4
address. Sailwright checks the guest agent first, then libvirt DHCP leases. If both
sources return no address, provisioning cannot build the temporary Ansible
inventory.

First confirm the VM is running and query the same libvirt connection Sailwright
uses:

```bash
virsh --connect qemu:///system domifaddr <domain> --source agent
virsh --connect qemu:///system domifaddr <domain> --source lease
```

If the agent lookup is empty, wait for the guest to finish booting and make sure
the image has `qemu-guest-agent` installed and running. If the lease lookup is
empty, make sure the VM is attached to a libvirt-managed network with visible
DHCP leases:

```bash
virsh --connect qemu:///system net-info default
virsh --connect qemu:///system net-dhcp-leases default
```

When the `default` network exists but is inactive, enable it:

```bash
sudo virsh --connect qemu:///system net-start default
sudo virsh --connect qemu:///system net-autostart default
```

For `DEV_ALCHEMY_LIBVIRT_URI=qemu:///session`, run the same checks against
`qemu:///session`. Session VMs use libvirt user-mode networking by default, so
guest-agent visibility is usually the most reliable IP source.

## Linux: apt Reports Release Files That Are "Not Valid Yet"

A libvirt VM that was paused, or whose host was suspended, resumes with the
time it had when it stopped. Until NTP catches up, apt rejects repository
metadata:

```text
E:Release file for http://archive.ubuntu.com/ubuntu/dists/noble-updates/InRelease is not valid yet (invalid for another 5h 9min 48s)
```

Before running Ansible against a Linux libvirt Ubuntu VM, Sailwright sets the
guest clock to the host time through the QEMU guest agent. If provisioning logs
`could not set the clock of libvirt VM ...`, the agent is not reachable; make sure
`qemu-guest-agent` is installed and running in the guest, or set the clock by
hand:

```bash
virsh --connect "${DEV_ALCHEMY_LIBVIRT_URI:-qemu:///system}" domtime <domain> --now
```

The domain is named `<os>-<type>-<arch>-dev-alchemy`, for example
`ubuntu-server-amd64-dev-alchemy`.

## `build list` Shows `incomplete` or `unverified`

`incomplete` means a build or pull of the target is running or was interrupted, or
the artifact no longer matches its `.sailwright-complete` marker. Run
`sailwright build <os> ...` again: it removes the leftovers of the interrupted build
and builds the target. `sailwright oci push` refuses `incomplete` artifacts.

`unverified` means the artifact has no completion marker, usually because an older
Sailwright version built it. It is still used. Rebuild it with `--no-cache` if it
turns out to be broken.

While a single build runs in the foreground, Packer owns the terminal and handles
Ctrl+C first; Sailwright counts it once Packer exits. Press Ctrl+C again after that
to skip the VNC video post-processing.
