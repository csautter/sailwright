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

var installWithGo bool
var installVirtualBox bool

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install host dependencies for the current operating system",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		hostOs := alchemy_build.GetCurrentHostOs()
		projectDir := alchemy_build.GetDirectoriesInstance().ProjectDir

		spec, err := installCommandForHost(hostOs, projectDir, installCommandOptions{withGo: installWithGo, virtualBox: installVirtualBox})
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
	rootCmd.AddCommand(installCmd)
}
