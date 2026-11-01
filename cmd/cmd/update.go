package cmd

import (
	"bufio"
	"context"
	"fmt"
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
		checks := ""
		settings, err := selfupdate.LoadSettings(config)
		if err != nil {
			checks = fmt.Sprintf("false (invalid settings: %v; fix with sailwright update --auto-check on|off)", err)
		} else {
			checks = fmt.Sprintf("%t (interval %s)", selfupdate.Enabled(settings, os.Getenv), settings.Interval)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "sailwright %s\nPlatform: %s/%s\nInstallation: %s (%s)\nAutomatic update checks: %s\nSettings: %s\n", version, runtime.GOOS, runtime.GOARCH, selfupdate.Installation(executable, version), executable, checks, config)
		return err
	}})
	var check, yes, noGateway bool
	var target, auto string
	var interval time.Duration
	update := &cobra.Command{Use: "update", Short: "Update Sailwright from a verified release", Args: cobra.NoArgs}
	update.Flags().BoolVar(&check, "check", false, "Only check (exit 0: current, 2: update available, 1: error)")
	update.Flags().BoolVar(&yes, "yes", false, "Update without confirmation")
	update.Flags().BoolVar(&noGateway, "no-gateway", false, "Do not update the adjacent gateway")
	update.Flags().StringVar(&target, "version", "", "Install a specific stable release (allows downgrade)")
	update.Flags().StringVar(&auto, "auto-check", "", "Enable or disable automatic notices: on|off")
	update.Flags().DurationVar(&interval, "auto-check-interval", selfupdate.DefaultInterval, "Automatic check interval (minimum 15m)")
	update.RunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("auto-check") || cmd.Flags().Changed("auto-check-interval") {
			if check || yes || noGateway || target != "" {
				return fmt.Errorf("update settings cannot be combined with release update options")
			}
			config, _, err := settingsPaths()
			if err != nil {
				return err
			}
			s, err := selfupdate.LoadSettings(config)
			if err != nil {
				// Start from defaults so a broken file can always be repaired from the CLI.
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: replacing invalid update settings: %v\n", err)
			}
			if cmd.Flags().Changed("auto-check") {
				switch auto {
				case "on":
					s.Enabled = true
				case "off":
					s.Enabled = false
				default:
					return fmt.Errorf("--auto-check must be on or off")
				}
			}
			if cmd.Flags().Changed("auto-check-interval") {
				s.Interval = interval
			}
			if err := selfupdate.SaveSettings(config, s); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Automatic update checks: %t (interval %s)\n", selfupdate.Enabled(s, os.Getenv), s.Interval)
			return nil
		}
		executable, err := selfupdate.Executable()
		if err != nil {
			return err
		}
		installation := selfupdate.Installation(executable, version)
		switch installation {
		case "homebrew":
			return fmt.Errorf("Homebrew manages this installation; use brew upgrade sailwright (and brew upgrade sailwright-gateway)")
		case "package-manager":
			return fmt.Errorf("use your package manager to update this installation")
		case "development":
			return fmt.Errorf("development builds cannot self-update; install a release with the install script")
		}
		if target != "" && !selfupdate.ValidVersion(target) {
			return fmt.Errorf("--version must be a stable release such as v1.2.3")
		}
		if !check {
			if err := selfupdate.Writable(executable); err != nil {
				return err
			}
		}
		release := releases()
		if err := selfupdate.ValidateBaseURL(release.BaseURL); err != nil {
			return err
		}
		if release.BaseURL != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: using non-default update source %s\n", release.BaseURL)
		}
		explicit := target != ""
		tag := target
		if explicit {
			tag = "v" + strings.TrimPrefix(tag, "v")
		} else {
			tag, err = release.Latest(cmd.Context())
			if err != nil {
				return err
			}
		}
		cmp := selfupdate.Compare(tag, version)
		if cmp == 0 || (!explicit && cmp < 0) {
			fmt.Fprintf(cmd.OutOrStdout(), "Sailwright %s is up to date.\n", version)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Sailwright %s → %s\nRelease notes: %s/releases/tag/%s\n", version, tag, selfupdate.Repository, tag)
		if check {
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			return updateAvailableError{}
		}
		if !yes {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("confirmation requires a terminal; pass --yes to update non-interactively")
			}
			fmt.Fprint(cmd.ErrOrStderr(), "Install this release? [y/N] ")
			reply, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if err != nil {
				return err
			}
			if strings.ToLower(strings.TrimSpace(reply)) != "y" && strings.ToLower(strings.TrimSpace(reply)) != "yes" {
				return fmt.Errorf("update cancelled")
			}
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()
		sums, err := release.Checksums(ctx, tag)
		if err != nil {
			return err
		}
		names := []string{"sailwright"}
		paths := []string{executable}
		if !noGateway {
			name := "sailwright-gateway"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			gateway := filepath.Join(filepath.Dir(executable), name)
			if info, err := os.Lstat(gateway); err == nil {
				if !info.Mode().IsRegular() {
					return fmt.Errorf("gateway must be a regular file in the same directory; use --no-gateway to skip it")
				}
				names = append(names, "sailwright-gateway")
				paths = append(paths, gateway)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		replacements := make([]selfupdate.Replacement, 0, len(names))
		for i, name := range names {
			data, err := release.Binary(ctx, tag, name, runtime.GOOS, runtime.GOARCH, sums)
			if err != nil {
				return err
			}
			replacements = append(replacements, selfupdate.Replacement{Path: paths[i], Data: data})
		}
		if err := selfupdate.Replace(replacements); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed Sailwright %s.\n", tag)
		if len(names) > 1 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Gateway updated. If sailwright-gateway is running, restart it to use the new version.")
		}
		return nil
	}
	rootCmd.AddCommand(update)
}

func startUpdateCheck() func() {
	noop := func() {}
	command, _, err := rootCmd.Find(os.Args[1:])
	if err != nil {
		return noop
	}
	if command.Run == nil && command.RunE == nil {
		return noop
	}
	name := command.Name()
	for _, arg := range os.Args[1:] {
		flag := strings.SplitN(arg, "=", 2)[0]
		if flag == "--help" || flag == "-h" || ((flag == "--version" || flag == "-v") && name != "update") {
			return noop
		}
	}
	if !selfupdate.Eligible(name, version, term.IsTerminal(int(os.Stderr.Fd())), os.Getenv) {
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
