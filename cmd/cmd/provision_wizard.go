package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/csautter/dev-alchemy/pkg/provision"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runWizardForm is replaceable so orchestration can be tested without a terminal.
var runWizardForm = func(cmd *cobra.Command, fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStdout()).RunWithContext(cmd.Context())
}

func provisionWizard(cmd *cobra.Command) error {
	if !isInteractiveInput(cmd.InOrStdin()) {
		return fmt.Errorf("choose a target: run `sailwright provision local --check` or `sailwright provision list`")
	}
	vms := availableProvisionVirtualMachines()
	targets := []huh.Option[int]{}
	for i, vm := range vms {
		if vm.OS == "local" {
			targets = append(targets, huh.NewOption(fmt.Sprintf("This computer (%s)", runtime.GOOS), i))
		}
	}
	for i, vm := range vms {
		if vm.OS != "local" {
			targets = append(targets, huh.NewOption(fmt.Sprintf("%s %s %s (VM)", vm.OS, vm.UbuntuType, vm.Arch), i))
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("no provisioning targets available")
	}
	target := targets[0].Value
	if err := runWizardForm(cmd, huh.NewSelect[int]().Title("Where do you want to install?").Options(targets...).Value(&target)); err != nil {
		return err
	}
	vm := vms[target]
	targetOS := vm.OS
	if targetOS == "local" {
		targetOS = runtime.GOOS
	}
	configPath, _ := provision.ConfigLocation()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		location := ""
		if err := runWizardForm(cmd, huh.NewInput().Title("Add your own tools folder or Git URL?").Description("Leave empty to use built-in examples only.").Value(&location)); err != nil {
			return err
		}
		if strings.TrimSpace(location) != "" {
			if err := provision.AddConfigSource(strings.TrimSpace(location), "", ""); err != nil {
				return err
			}
		}
	}
	entries, err := provision.Catalog(targetOS, false)
	if err != nil {
		return err
	}
	kind := "role"
	if err := runWizardForm(cmd, huh.NewSelect[string]().Title("What do you want to install?").Options(huh.NewOption("Tools (choose one or more roles)", "role"), huh.NewOption("A complete setup (one playbook)", "playbook")).Value(&kind)); err != nil {
		return err
	}
	options := []huh.Option[string]{}
	for _, e := range entries {
		if e.Kind != kind {
			continue
		}
		label := fmt.Sprintf("%s — %s (%s)", e.Name, e.Description, e.Source)
		if !e.Compatible {
			fmt.Fprintf(cmd.OutOrStdout(), "Unavailable for %s: %s [incompatible]\n", targetOS, label)
			continue
		}
		options = append(options, huh.NewOption(label, e.Name))
	}
	if len(options) == 0 {
		return fmt.Errorf("no compatible %ss available; add a source with `sailwright config sources add <folder|git-url>`", kind)
	}
	previousRoles, previousPlaybook, previousCheck, previousYes, previousAsk, previousArch, previousType := selectedRoles, playbookPath, check, assumeYes, askBecomePass, arch, osType
	defer func() {
		selectedRoles, playbookPath, check, assumeYes, askBecomePass, arch, osType = previousRoles, previousPlaybook, previousCheck, previousYes, previousAsk, previousArch, previousType
	}()
	selectedRoles = nil
	playbookPath = ""
	if kind == "role" {
		if err := runWizardForm(cmd, huh.NewMultiSelect[string]().Title("Choose tools").Description("Type / to filter; space to select.").Options(options...).Filterable(true).Value(&selectedRoles).Validate(func(v []string) error {
			if len(v) == 0 {
				return fmt.Errorf("select at least one tool")
			}
			return nil
		})); err != nil {
			return err
		}
	} else {
		if err := runWizardForm(cmd, huh.NewSelect[string]().Title("Choose a setup").Options(options...).Value(&playbookPath)); err != nil {
			return err
		}
	}
	preview := true
	if err := runWizardForm(cmd, huh.NewConfirm().Title("Preview first (no changes)?").Value(&preview)); err != nil {
		return err
	}
	if !askBecomePass && vm.OS == "local" && vm.HostOs != alchemy_build.HostOsWindows {
		askBecomePass = wizardNeedsSudoPassword()
	}
	if vm.OS == "local" && vm.HostOs == alchemy_build.HostOsWindows && !assumeYes {
		approved := false
		fmt.Fprintln(cmd.OutOrStdout(), localWindowsProvisionConfirmationMessage(provision.ProvisionOptions{LocalWindowsProtocol: provision.LocalWindowsProvisionProtocol(localProvisionProto)}))
		if err := runWizardForm(cmd, huh.NewConfirm().Title("Allow temporary Windows setup and UAC elevation?").Value(&approved)); err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("local Windows provisioning cancelled")
		}
		assumeYes = true
	}
	arch, osType = vm.Arch, vm.UbuntuType
	check = preview
	defer func() {
		fmt.Fprintln(cmd.OutOrStdout(), "Tip: re-run without questions: "+wizardCommand(cmd, vm, selectedRoles, playbookPath, check, askBecomePass, assumeYes))
	}()
	if preview {
		if err := cmd.RunE(cmd, []string{vm.OS}); err != nil {
			return err
		}
	}
	apply := false
	if err := runWizardForm(cmd, huh.NewConfirm().Title("Apply these changes now?").Value(&apply)); err != nil {
		return err
	}
	if apply {
		check = false
		return cmd.RunE(cmd, []string{vm.OS})
	}

	return nil
}

func wizardNeedsSudoPassword() bool {
	if current, err := user.Current(); err == nil && current.Uid == "0" {
		return false
	}
	return exec.Command("sudo", "-n", "true").Run() != nil
}

func wizardCommand(cmd *cobra.Command, vm alchemy_build.VirtualMachineConfig, roles []string, playbook string, preview, ask, yes bool) string {
	args := []string{"sailwright", "provision", vm.OS}
	if vm.OS != "local" {
		args = append(args, "--arch", vm.Arch)
		if vm.UbuntuType != "" {
			args = append(args, "--type", vm.UbuntuType)
		}
	}
	for _, r := range roles {
		args = append(args, "--role", r)
	}
	if playbook != "" {
		args = append(args, "--playbook", playbook)
	}
	if preview {
		args = append(args, "--check")
	}
	if ask {
		args = append(args, "--ask-become-pass")
	}
	if yes {
		args = append(args, "--yes")
	}
	skip := map[string]bool{"arch": true, "type": true, "role": true, "playbook": true, "check": true, "dry-run": true, "ask-become-pass": true, "yes": true}
	cmd.LocalFlags().Visit(func(f *pflag.Flag) {
		if !skip[f.Name] {
			args = append(args, "--"+f.Name+"="+f.Value.String())
		}
	})
	cmd.InheritedFlags().Visit(func(f *pflag.Flag) { args = append(args, "--"+f.Name+"="+f.Value.String()) })
	for i, a := range args {
		if strings.ContainsAny(a, " \t\n'\"$`\\;&|<>()") {
			if runtime.GOOS == "windows" {
				args[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
			} else {
				args[i] = "'" + strings.ReplaceAll(a, "'", "'\"'\"'") + "'"
			}
		}
	}
	return strings.Join(args, " ")
}
