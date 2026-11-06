package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/csautter/dev-alchemy/internal/selfupdate"
	"github.com/csautter/dev-alchemy/pkg/build"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// version is injected by release builds through -ldflags -X.
var version = "dev"

const settingsRepairHint = "fix with sailwright update --auto-check on|off"

type updateAvailableError struct{}

func (updateAvailableError) Error() string { return "update available" }

func releases() selfupdate.Releases {
	return selfupdate.Releases{Version: version, BaseURL: os.Getenv("SAILWRIGHT_UPDATE_BASE_URL")}
}

func settingsPaths() (string, string, error) {
	dirs, err := build.UserSettingsDirectories()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dirs.ConfigDir, "update.json"), filepath.Join(dirs.CacheDir, "update-check.json"), nil
}

// updateOptions holds the parsed flags of the update command.
type updateOptions struct {
	check, yes, noGateway, requireSignature bool
	target, auto                            string
	interval                                time.Duration
	autoChanged, intervalChanged            bool
}

func (o updateOptions) changesSettings() bool { return o.autoChanged || o.intervalChanged }

func (o updateOptions) hasReleaseOptions() bool {
	return o.check || o.yes || o.noGateway || o.requireSignature || o.target != ""
}

// updateEnv carries the process state the update command depends on, so its
// decisions can be tested without a real executable, terminal or network.
type updateEnv struct {
	in           io.Reader
	out, errOut  io.Writer
	interactive  bool
	executable   string
	version      string
	goos, goarch string
	settingsFile string
	getenv       func(string) string
	releases     selfupdate.Releases
	replace      func([]selfupdate.Replacement) error
}

func init() {
	rootCmd.Version = version
	rootCmd.AddCommand(&cobra.Command{Use: "version", Short: "Show version, platform, installation and update settings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		executable, err := selfupdate.Executable()
		if err != nil {
			return err
		}
		config, _, err := settingsPaths()
		if err != nil {
			return err
		}
		return writeVersion(cmd.OutOrStdout(), executable, config, os.Getenv)
	}})

	var o updateOptions
	update := &cobra.Command{Use: "update", Short: "Update Sailwright from a checksum-verified release (cosign signature when available)", Args: cobra.NoArgs}
	update.Flags().BoolVar(&o.check, "check", false, "Only check (exit 0: current, 2: update available, 1: error)")
	update.Flags().BoolVar(&o.requireSignature, "require-signature", false, "Require cosign signature verification (fails if cosign is unavailable)")
	update.Flags().BoolVar(&o.yes, "yes", false, "Update without confirmation")
	update.Flags().BoolVar(&o.noGateway, "no-gateway", false, "Do not update the adjacent gateway")
	update.Flags().StringVar(&o.target, "version", "", "Install a specific stable release (allows downgrade)")
	update.Flags().StringVar(&o.auto, "auto-check", "", "Enable or disable automatic notices: on|off")
	update.Flags().DurationVar(&o.interval, "auto-check-interval", selfupdate.DefaultInterval, "Automatic check interval (minimum 15m)")
	update.RunE = func(cmd *cobra.Command, _ []string) error {
		o.autoChanged = cmd.Flags().Changed("auto-check")
		o.intervalChanged = cmd.Flags().Changed("auto-check-interval")
		config, _, err := settingsPaths()
		if err != nil {
			return err
		}
		env := updateEnv{
			in: cmd.InOrStdin(), out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr(),
			interactive: term.IsTerminal(int(os.Stdin.Fd())),
			version:     version, goos: runtime.GOOS, goarch: runtime.GOARCH,
			settingsFile: config, getenv: os.Getenv,
			releases: releases(), replace: selfupdate.Replace,
		}
		if o.changesSettings() {
			return runUpdateSettings(o, env)
		}
		if env.executable, err = selfupdate.Executable(); err != nil {
			return err
		}
		err = runUpdate(cmd.Context(), o, env)
		if _, ok := err.(updateAvailableError); ok {
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
		}
		return err
	}
	rootCmd.AddCommand(update)
}

func writeVersion(w io.Writer, executable, config string, getenv func(string) string) error {
	checks, signature := "", "when cosign is installed"
	settings, err := selfupdate.LoadSettings(config)
	if err != nil {
		checks = fmt.Sprintf("false (invalid settings: %v; %s)", err, settingsRepairHint)
		signature = "unknown (invalid settings)"
	} else {
		checks = fmt.Sprintf("%t (interval %s)", selfupdate.Enabled(settings, getenv), settings.Interval)
		if settings.RequireSignature {
			signature = "required"
		}
	}
	_, err = fmt.Fprintf(w, "sailwright %s\nPlatform: %s/%s\nInstallation: %s (%s)\nAutomatic update checks: %s\nSignature verification: %s\nSettings: %s\n",
		version, runtime.GOOS, runtime.GOARCH, selfupdate.Installation(executable, version), executable, checks, signature, config)
	return err
}

func runUpdateSettings(o updateOptions, env updateEnv) error {
	if o.hasReleaseOptions() {
		return fmt.Errorf("update settings cannot be combined with release update options")
	}
	s, err := selfupdate.LoadSettings(env.settingsFile)
	if err != nil {
		// Start from defaults so a broken file can always be repaired from the CLI.
		fmt.Fprintf(env.errOut, "Warning: replacing invalid update settings: %v\n", err)
	}
	if o.autoChanged {
		switch o.auto {
		case "on":
			s.Enabled = true
		case "off":
			s.Enabled = false
		default:
			return fmt.Errorf("--auto-check must be on or off")
		}
	}
	if o.intervalChanged {
		s.Interval = o.interval
	}
	if err := selfupdate.SaveSettings(env.settingsFile, s); err != nil {
		return err
	}
	fmt.Fprintf(env.out, "Automatic update checks: %t (interval %s)\n", selfupdate.Enabled(s, env.getenv), s.Interval)
	return nil
}

func runUpdate(ctx context.Context, o updateOptions, env updateEnv) error {
	switch selfupdate.Installation(env.executable, env.version) {
	case "homebrew":
		return fmt.Errorf("Homebrew manages this installation; use brew upgrade sailwright (and brew upgrade sailwright-gateway)")
	case "package-manager":
		return fmt.Errorf("use your package manager to update this installation")
	case "development":
		return fmt.Errorf("development builds cannot self-update; install a release with the install script")
	}
	if o.target != "" && !selfupdate.ValidVersion(o.target) {
		return fmt.Errorf("--version must be a stable release such as v1.2.3")
	}
	names, paths, err := updateTargets(env.executable, env.goos, o.noGateway, !o.check)
	if err != nil {
		return err
	}
	release := env.releases
	settings, err := selfupdate.LoadSettings(env.settingsFile)
	if err != nil {
		return fmt.Errorf("%w; %s", err, settingsRepairHint)
	}
	release.RequireSignature = o.requireSignature || settings.RequireSignature
	if err := selfupdate.ValidateBaseURL(release.BaseURL); err != nil {
		return err
	}
	if release.BaseURL != "" {
		fmt.Fprintf(env.errOut, "Warning: using non-default update source %s\n", release.BaseURL)
	}
	explicit := o.target != ""
	tag := "v" + strings.TrimPrefix(o.target, "v")
	if !explicit {
		if tag, err = release.Latest(ctx); err != nil {
			return err
		}
	}
	cmp := selfupdate.Compare(tag, env.version)
	if cmp == 0 || (!explicit && cmp < 0) {
		fmt.Fprintf(env.out, "Sailwright %s is up to date.\n", env.version)
		return nil
	}
	fmt.Fprintf(env.out, "Sailwright %s → %s\nRelease notes: %s/releases/tag/%s\n", env.version, tag, selfupdate.Repository, tag)
	if o.check {
		return updateAvailableError{}
	}
	downgrade := cmp < 0
	if downgrade {
		fmt.Fprintln(env.errOut, "Warning: releases older than the first one with `sailwright update` cannot update themselves; rerun the install script to return to a newer release.")
	}
	if err := confirmUpdate(env, o.yes, downgrade); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	sums, signed, err := release.Checksums(ctx, tag)
	if err != nil {
		return err
	}
	if signed {
		fmt.Fprintln(env.errOut, "Checksum manifest signature verified with cosign.")
	} else {
		fmt.Fprintln(env.errOut, "Warning: cosign not found; only SHA-256 checksums were verified, not the release signature. Install cosign or use --require-signature to enforce it.")
	}
	replacements := make([]selfupdate.Replacement, 0, len(names))
	for i, name := range names {
		data, err := release.Binary(ctx, tag, name, env.goos, env.goarch, sums)
		if err != nil {
			return err
		}
		replacements = append(replacements, selfupdate.Replacement{Path: paths[i], Data: data})
	}
	if err := env.replace(replacements); err != nil {
		return err
	}
	fmt.Fprintf(env.out, "Installed Sailwright %s.\n", tag)
	if len(names) > 1 {
		fmt.Fprintln(env.errOut, "Gateway updated. If sailwright-gateway is running, restart it to use the new version.")
	}
	return nil
}

// updateTargets lists the release binaries to replace: the CLI and, unless
// skipped, an adjacent gateway. writable probes the directories before any
// network request.
func updateTargets(executable, goos string, noGateway, writable bool) (names, paths []string, err error) {
	if writable {
		if err := selfupdate.Writable(executable); err != nil {
			return nil, nil, err
		}
	}
	names, paths = []string{"sailwright"}, []string{executable}
	if noGateway {
		return names, paths, nil
	}
	name := "sailwright-gateway"
	if goos == "windows" {
		name += ".exe"
	}
	gateway := filepath.Join(filepath.Dir(executable), name)
	info, err := os.Lstat(gateway)
	if os.IsNotExist(err) {
		return names, paths, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("gateway must be a regular file in the same directory; use --no-gateway to skip it")
	}
	if writable {
		if err := selfupdate.Writable(gateway); err != nil {
			return nil, nil, err
		}
	}
	return append(names, "sailwright-gateway"), append(paths, gateway), nil
}

// confirmUpdate asks before installing. Downgrades always need an interactive
// confirmation, even with --yes.
func confirmUpdate(env updateEnv, yes, downgrade bool) error {
	if downgrade && !env.interactive {
		return fmt.Errorf("downgrades require an interactive terminal and confirmation")
	}
	if yes && !downgrade {
		return nil
	}
	if !env.interactive {
		return fmt.Errorf("confirmation requires a terminal; pass --yes to update non-interactively")
	}
	fmt.Fprint(env.errOut, "Install this release? [y/N] ")
	reply, err := bufio.NewReader(env.in).ReadString('\n')
	if err != nil && reply == "" {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "y", "yes":
		return nil
	}
	return fmt.Errorf("update cancelled")
}

// updateCheckApplies reports whether an invocation may run a background check:
// only runnable subcommands, never help or the root version flag.
func updateCheckApplies(command *cobra.Command, args []string) bool {
	if command == rootCmd || (command.Run == nil && command.RunE == nil) {
		return false
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if flag := strings.SplitN(arg, "=", 2)[0]; flag == "--help" || flag == "-h" {
			return false
		}
	}
	return true
}

func startUpdateCheck() func() {
	noop := func() {}
	command, _, err := rootCmd.Find(os.Args[1:])
	if err != nil || !updateCheckApplies(command, os.Args[1:]) {
		return noop
	}
	if !selfupdate.Eligible(command.Name(), version, term.IsTerminal(int(os.Stderr.Fd())), os.Getenv) {
		return noop
	}
	config, cache, err := settingsPaths()
	if err != nil {
		return noop
	}
	s, err := selfupdate.LoadSettings(config)
	if err != nil {
		return noop
	}
	s.Enabled = selfupdate.Enabled(s, os.Getenv)
	if !s.Enabled {
		return noop
	}
	finish := (selfupdate.Checker{Releases: releases(), Settings: s, CacheFile: cache}).Start()
	return func() {
		if latest := finish(); latest != "" {
			action := "sailwright update"
			if exe, err := selfupdate.Executable(); err == nil && selfupdate.Installation(exe, version) == "homebrew" {
				action = "brew upgrade sailwright"
			}
			fmt.Fprintf(os.Stderr, "Sailwright %s is available; run `%s`.\n", latest, action)
		}
	}
}
