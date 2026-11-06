package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/csautter/dev-alchemy/internal/winpsenv"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
)

type installCommandSpec struct {
	executable string
	args       []string
	scriptPath string
}

type installCommandOptions struct {
	withGo     bool
	virtualBox bool
	hyperV     bool
	vagrant    bool
}

var runInstallCommand = func(spec installCommandSpec) error {
	if _, err := os.Stat(spec.scriptPath); err != nil {
		return fmt.Errorf("install script not found at %q: %w", spec.scriptPath, err)
	}

	// #nosec G204 -- installCommandForHost selects a fixed interpreter and repo-local script path for the detected host OS.
	cmd := exec.Command(spec.executable, spec.args...)
	cmd.Env = winpsenv.CommandEnv(spec.executable, nil)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed running install script %q with %s: %w", spec.scriptPath, spec.executable, err)
	}

	return nil
}

func installCommandForHost(hostOs alchemy_build.HostOsType, projectDir string, options installCommandOptions) (installCommandSpec, error) {
	if options.virtualBox && hostOs != alchemy_build.HostOsWindows {
		return installCommandSpec{}, fmt.Errorf("install --virtualbox is only supported for host OS: %s", hostOs)
	}
	if options.hyperV && hostOs != alchemy_build.HostOsWindows {
		return installCommandSpec{}, fmt.Errorf("install --hyperv is only supported for host OS: %s", alchemy_build.HostOsWindows)
	}
	if options.vagrant && hostOs != alchemy_build.HostOsWindows {
		return installCommandSpec{}, fmt.Errorf("install --vagrant is only supported for host OS: %s", alchemy_build.HostOsWindows)
	}

	switch hostOs {
	case alchemy_build.HostOsDarwin:
		scriptPath := filepath.Join(projectDir, "scripts", "macos", "sailwright-install-dependencies.sh")
		args := []string{scriptPath}
		if options.withGo {
			args = append(args, "--with-go")
		}
		return installCommandSpec{
			executable: "bash",
			args:       args,
			scriptPath: scriptPath,
		}, nil
	case alchemy_build.HostOsWindows:
		scriptPath := filepath.Join(projectDir, "scripts", "windows", "sailwright-self-setup.ps1")
		args := []string{"-ExecutionPolicy", "Bypass", "-File", scriptPath}
		if options.withGo {
			args = append(args, "-WithGo")
		}
		if options.virtualBox {
			args = append(args, "-VirtualBox")
		}
		if options.hyperV {
			args = append(args, "-HyperV")
		}
		if options.vagrant {
			args = append(args, "-Vagrant")
		}
		return installCommandSpec{
			executable: "powershell",
			args:       args,
			scriptPath: scriptPath,
		}, nil
	case alchemy_build.HostOsLinux:
		scriptPath := filepath.Join(projectDir, "scripts", "linux", "sailwright-install-dependencies.sh")
		args := []string{scriptPath}
		if options.withGo {
			args = append(args, "--with-go")
		}
		return installCommandSpec{
			executable: "bash",
			args:       args,
			scriptPath: scriptPath,
		}, nil
	default:
		return installCommandSpec{}, fmt.Errorf("install is not supported for host OS: %s", hostOs)
	}
}

const installHyperVHint = "Hyper-V is only needed to build, create or run sailwright VMs with the Hyper-V engine on this Windows host. Local provisioning (sailwright provision local) and VirtualBox VMs work without it. Enabling Hyper-V needs hardware virtualization (nested virtualization when Windows itself runs in a VM) and a Windows restart; it also installs oscdimg from the Windows ADK for Hyper-V builds."

const installVagrantHint = "Vagrant is only needed to create, start, stop or destroy sailwright VMs on this Windows host (the Hyper-V engine drives VMs through Vagrant). Local provisioning (sailwright provision local) works without it."

// installOptIn describes a Windows-only installer component that is off by
// default and offered interactively.
type installOptIn struct {
	flag     string
	label    string
	hint     string
	question string
}

var (
	installHyperVOptIn  = installOptIn{flag: "hyperv", label: "Hyper-V setup", hint: installHyperVHint, question: "Enable Hyper-V now?"}
	installVagrantOptIn = installOptIn{flag: "vagrant", label: "Vagrant installation", hint: installVagrantHint, question: "Install Vagrant now?"}
)

// resolveInstallOptIn decides whether an opt-in Windows component is
// installed: an explicit --flag / --flag=false wins, an interactive terminal
// is asked (default no), and non-interactive runs such as the agent gateway
// skip it with a hint.
func resolveInstallOptIn(cmd *cobra.Command, hostOs alchemy_build.HostOsType, optIn installOptIn, flagValue bool) (bool, error) {
	if cmd.Flags().Changed(optIn.flag) || hostOs != alchemy_build.HostOsWindows {
		return flagValue, nil
	}

	if !isInteractiveInput(cmd.InOrStdin()) {
		fmt.Fprintf(cmd.OutOrStdout(), "ℹ️  Skipping %s. %s Run \"sailwright install --%s\" to add it.\n", optIn.label, optIn.hint, optIn.flag)
		return false, nil
	}

	confirmed, err := promptForConfirmationFunc(cmd.InOrStdin(), cmd.OutOrStdout(), optIn.hint+" "+optIn.question+" [y/N]: ")
	if err != nil {
		return false, fmt.Errorf("failed to read the answer for --%s: %w", optIn.flag, err)
	}
	if !confirmed {
		fmt.Fprintf(cmd.OutOrStdout(), "Skipping %s. Run \"sailwright install --%s\" to add it later.\n", optIn.label, optIn.flag)
	}
	return confirmed, nil
}

func resolveInstallHyperV(cmd *cobra.Command, hostOs alchemy_build.HostOsType) (bool, error) {
	return resolveInstallOptIn(cmd, hostOs, installHyperVOptIn, installHyperV)
}

func resolveInstallVagrant(cmd *cobra.Command, hostOs alchemy_build.HostOsType) (bool, error) {
	return resolveInstallOptIn(cmd, hostOs, installVagrantOptIn, installVagrant)
}

var installWithGo bool
var installVirtualBox bool
var installHyperV bool
var installVagrant bool

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install host dependencies for the current operating system",
	Long: `Install host dependencies for the current operating system.

On Windows, Hyper-V is opt-in: pass --hyperv to enable it (the optional
features on Windows client editions, the Hyper-V role on Windows Server), or
answer the prompt in an interactive terminal. Hyper-V is only needed for VMs
with the Hyper-V engine. Enabling it needs a Windows restart; run
"sailwright install --hyperv" again afterwards to verify the setup.

Vagrant is opt-in on Windows as well: pass --vagrant or answer the prompt.
It is needed to create and run sailwright VMs, not for local provisioning.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		hostOs := alchemy_build.GetCurrentHostOs()
		projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir

		hyperV, err := resolveInstallHyperV(cmd, hostOs)
		if err != nil {
			return err
		}
		vagrant, err := resolveInstallVagrant(cmd, hostOs)
		if err != nil {
			return err
		}

		spec, err := installCommandForHost(hostOs, projectDir, installCommandOptions{withGo: installWithGo, virtualBox: installVirtualBox, hyperV: hyperV, vagrant: vagrant})
		if err != nil {
			return err
		}

		fmt.Printf("🔧 Running dependency installer for host OS: %s\n", hostOs)
		return runInstallCommand(spec)
	},
}

func init() {
	installCmd.Flags().BoolVar(&installWithGo, "with-go", false, "Also install the Go toolchain when supported on the current host OS")
	installCmd.Flags().BoolVar(&installVirtualBox, "virtualbox", false, "Also install VirtualBox on Windows hosts")
	installCmd.Flags().BoolVar(&installHyperV, "hyperv", false, "Also enable Hyper-V on Windows hosts (asked interactively when omitted; needs a restart)")
	installCmd.Flags().BoolVar(&installVagrant, "vagrant", false, "Also install Vagrant on Windows hosts, needed to create and run VMs (asked interactively when omitted)")
	rootCmd.AddCommand(installCmd)
}
