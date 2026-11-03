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

// wizardSelection is the state collected by the wizard's phases. Running it
// builds provisionRequest values instead of changing package-level flags.
type wizardSelection struct {
	vm            alchemy_build.VirtualMachineConfig
	targetOS      string
	roles         []string
	playbook      string
	preview       bool
	askBecomePass bool
	assumeYes     bool
}

func provisionWizard(cmd *cobra.Command) error {
	if !isInteractiveInput(cmd.InOrStdin()) {
		return fmt.Errorf("choose a target: run `sailwright provision local --check` or `sailwright provision list`")
	}
	sel := wizardSelection{askBecomePass: askBecomePass, assumeYes: assumeYes}
	if err := wizardSelectTarget(cmd, &sel); err != nil {
		return err
	}
	if err := wizardOfferSource(cmd); err != nil {
		return err
	}
	if err := wizardSelectContent(cmd, &sel); err != nil {
		return err
	}
	if err := wizardConfirmExecution(cmd, &sel); err != nil {
		return err
	}
	return wizardExecute(cmd, sel)
}

// wizardSelectTarget asks where to install, listing this computer first.
func wizardSelectTarget(cmd *cobra.Command, sel *wizardSelection) error {
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
	sel.vm = vms[target]
	sel.targetOS = sel.vm.OS
	if sel.targetOS == "local" {
		sel.targetOS = runtime.GOOS
	}
	return nil
}

// wizardOfferSource offers to add a first source when no config exists yet.
func wizardOfferSource(cmd *cobra.Command) error {
	configPath, _ := provision.ConfigLocation()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		return nil
	}
	location := ""
	if err := runWizardForm(cmd, huh.NewInput().Title("Add your own tools folder or Git URL?").Description("Leave empty to use built-in examples only.").Value(&location)); err != nil {
		return err
	}
	if strings.TrimSpace(location) == "" {
		return nil
	}
	return provision.AddConfigSource(strings.TrimSpace(location), "", "")
}

// wizardSelectContent asks for roles or one playbook from the catalog.
func wizardSelectContent(cmd *cobra.Command, sel *wizardSelection) error {
	entries, err := provision.Catalog(sel.targetOS, false)
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
			fmt.Fprintf(cmd.OutOrStdout(), "Unavailable for %s: %s [incompatible]\n", sel.targetOS, label)
			continue
		}
		options = append(options, huh.NewOption(label, e.Name))
	}
	if len(options) == 0 {
		return fmt.Errorf("no compatible %ss available; add a source with `sailwright config sources add <folder|git-url>`", kind)
	}
	if kind == "playbook" {
		return runWizardForm(cmd, huh.NewSelect[string]().Title("Choose a setup").Options(options...).Value(&sel.playbook))
	}
	return runWizardForm(cmd, huh.NewMultiSelect[string]().Title("Choose tools").Description("Type / to filter; space to select.").Options(options...).Filterable(true).Value(&sel.roles).Validate(func(v []string) error {
		if len(v) == 0 {
			return fmt.Errorf("select at least one tool")
		}
		return nil
	}))
}

// wizardConfirmExecution asks about preview, sudo and Windows elevation.
func wizardConfirmExecution(cmd *cobra.Command, sel *wizardSelection) error {
	sel.preview = true
	if err := runWizardForm(cmd, huh.NewConfirm().Title("Preview first (no changes)?").Value(&sel.preview)); err != nil {
		return err
	}
	if sel.vm.OS != "local" {
		return nil
	}
	if sel.vm.HostOs != alchemy_build.HostOsWindows {
		if !sel.askBecomePass {
			sel.askBecomePass = wizardNeedsSudoPasswordFunc()
		}
		return nil
	}
	if sel.assumeYes {
		return nil
	}
	approved := false
	fmt.Fprintln(cmd.OutOrStdout(), localWindowsProvisionConfirmationMessage(provision.ProvisionOptions{LocalWindowsProtocol: provision.LocalWindowsProvisionProtocol(localProvisionProto)}))
	if err := runWizardForm(cmd, huh.NewConfirm().Title("Allow temporary Windows setup and UAC elevation?").Value(&approved)); err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("local Windows provisioning cancelled")
	}
	sel.assumeYes = true
	return nil
}

// wizardExecute runs the optional preview, asks to apply, and prints a
// reusable command line.
func wizardExecute(cmd *cobra.Command, sel wizardSelection) error {
	request := func(check bool) provisionRequest {
		return provisionRequest{
			target:           sel.vm.OS,
			roles:            sel.roles,
			playbook:         sel.playbook,
			playbookExplicit: sel.playbook != "",
			arch:             sel.vm.Arch,
			osType:           sel.vm.UbuntuType,
			check:            check,
			assumeYes:        sel.assumeYes,
			askBecomePass:    sel.askBecomePass,
		}
	}
	applied := false
	defer func() {
		fmt.Fprintln(cmd.OutOrStdout(), "Tip: re-run without questions: "+wizardCommand(cmd, sel.vm, sel.roles, sel.playbook, !applied, sel.askBecomePass, sel.assumeYes))
	}()
	if sel.preview {
		if err := executeProvision(cmd, request(true)); err != nil {
			return err
		}
	}
	apply := false
	if err := runWizardForm(cmd, huh.NewConfirm().Title("Apply these changes now?").Value(&apply)); err != nil {
		return err
	}
	if !apply {
		return nil
	}
	applied = true
	return executeProvision(cmd, request(false))
}

// wizardNeedsSudoPasswordFunc is replaceable so tests never invoke sudo.
var wizardNeedsSudoPasswordFunc = wizardNeedsSudoPassword

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
