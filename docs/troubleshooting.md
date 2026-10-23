# Troubleshooting

This page collects rare, host-specific issues that do not affect most Sailwright setups.

## Windows 11 ISO Missing: "Microsoft download disabled in CI"

A Windows 11 build fails with `Windows 11 ISO missing at …; Microsoft download disabled in CI`
when `SAILWRIGHT_WIN11_ISO_OFFLINE` is `1` or `true` and the ISO is not in the cache. CI build jobs
set this variable on purpose. Locally, unset it (it is often copied from a workflow's `env`) so the
ISO is downloaded from Microsoft, or place the ISO at
`<cache dir>/windows11/iso/win11_english_<arch>.iso`. For the CI side, see
[Windows 11 ISO cache](./windows-iso-cache.md).

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
