# Local Provisioning

Use `sailwright provision local` when you want Sailwright to run the configured
default playbook against the current machine instead of a managed VM or remote
host.

For day-to-day machine setup, define that default in
`ansible-role-sources.yml`. Without a configured default, Sailwright falls back
to the bundled `./playbooks/role-sources-test.yml`, which is a quick
role-source smoke/example playbook. To run the broader bundled example setup
directly, pass `--playbook ./playbooks/setup.yml`.

Start with the root [README](../README.md) for installation and discovery, then
use this guide for wrapper-specific behavior and flags.

## Quick examples

```bash
sailwright provision local --check
sailwright provision local --ask-become-pass
sailwright provision local --proto ssh --check
sailwright provision local --playbook ./playbooks/setup.yml --check
sailwright provision local --playbook ./playbooks/setup.yml
sailwright provision local -- --diff --tags java
sailwright provision local --inventory-path ./inventory/remote.yml --ask-become-pass -- --limit workstation
sailwright provision local --check --yes
sailwright provision local --check --yes --force-winrm-uninstall
sailwright provision local --proto ssh --check --yes --force-ssh-uninstall
sailwright provision local
```

## What the wrapper does

- Selects a default localhost inventory for the current host OS.
- Resolves the playbook from `--playbook`, `ansible-role-sources.yml`, or the
  bundled role-source smoke fallback.
- Passes extra `ansible-playbook` flags through when you place them after `--`.

Use `--playbook` to point at a different playbook file. Use
`--inventory-path` to override the default local inventory file.

## Platform defaults

- macOS and Linux use `inventory/localhost.yaml`.
- Windows uses `inventory/localhost_windows_winrm.yml` by default.
- Windows `--proto ssh` switches to `inventory/localhost_windows_ssh.yml`.

The macOS/Linux local target is currently marked unstable until it has been
validated end-to-end.

## sudo password on macOS and Linux

Roles that install system packages run their tasks with `become`, which uses
`sudo`. If `sudo` asks for a password on your machine, add `--ask-become-pass`
(short: `-K`) and Sailwright prompts for it once before the run:

```bash
sailwright provision local --ask-become-pass
sailwright provision local -K --check
```

The prompt needs an interactive terminal. For scripted runs, pass a password
file to Ansible instead: `sailwright provision local -- --become-password-file
<path>`. The flag is not available for local Windows provisioning, which
connects through a temporary administrator account, or for VM targets, where
Sailwright sets the credentials itself.

## Windows local behavior

The Windows wrapper makes temporary host changes so Ansible can reach the local
machine safely for the duration of the run.

In the default WinRM mode, the wrapper:

- Creates a temporary administrator account with a random password.
- Creates a temporary loopback-only WinRM HTTPS listener for the run.
- Restores the previous WinRM state during cleanup.

With `--proto ssh`, the wrapper:

- Creates or updates a temporary administrator account with a temporary SSH
  key.
- Enables or installs OpenSSH Server when needed.
- Sets the default SSH shell to PowerShell for the run.
- Restores the prior SSH service, firewall, `authorized_keys`, and shell state
  afterwards.

If the wrapper had to install OpenSSH Server, cleanup disables `sshd` but
leaves the OpenSSH Server capability installed so cleanup does not require a
reboot.

If the `devalchemy_ansible` account already exists, the SSH wrapper reuses it
as the automation account and rotates its password for the run. Cleanup does
not restore the previous password, so treat that account as automation-managed
rather than a hand-managed login.

Because these are significant host changes, the Windows local flow asks for
confirmation by default. Use `--yes` to skip those CLI confirmation prompts.

On Windows, local provisioning is only fully non-interactive when you start
`sailwright` from an already elevated shell. If the current shell is not
elevated, the privileged bootstrap and cleanup steps still trigger a UAC prompt
before they run.

Bootstrap and cleanup logs are streamed back into the main terminal.

For manual Windows transport setup and rollback guidance, see
[Windows Ansible Access](./windows-ansible-access.md).

## Cleanup flags

`--force-winrm-uninstall` is only for the default WinRM mode. It forces
cleanup to disable WinRM and remove transient remoting setup after the run.

`--force-ssh-uninstall` is only for `--proto ssh`. It forces cleanup to
disable `sshd`, remove SSH firewall rules, and remove the transient Ansible
user after the run without uninstalling OpenSSH Server.

If you need to remove an OpenSSH Server capability that the wrapper installed,
follow the manual rollback steps in
[Windows Ansible Access](./windows-ansible-access.md#remove-openssh-server-after-a-wrapper-run).
