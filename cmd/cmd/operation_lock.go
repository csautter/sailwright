package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/csautter/dev-alchemy/internal/operationlock"
	alchemy_build "github.com/csautter/dev-alchemy/pkg/build"
	"github.com/spf13/cobra"
)

// lockTimeoutEnvVar sets how long an operation waits for a busy target (Go duration,
// default 0: fail immediately). sailwright-gateway sets it for its jobs.
const lockTimeoutEnvVar = "SAILWRIGHT_LOCK_TIMEOUT"

var wrapOperationLocksOnce sync.Once

// operationCommands are the commands that change VM state or the artifact cache.
func operationCommands() []*cobra.Command {
	return []*cobra.Command{buildCmd, createCmd, startCmd, stopCmd, destroyCmd, provisionCmd, pullCmd, pushCmd}
}

// installOperationLocks wraps the operation commands once, at Execute time, so the
// wrapper never captures a RunE that a later init has not assigned yet. Tests that
// call RunE directly do not take locks in the developer's real app-data.
func installOperationLocks() {
	wrapOperationLocksOnce.Do(func() {
		wrapOperationLocks(operationCommands(), func() string { return alchemy_build.GetDirectoriesInstance().AppDataDir })
	})
}

func wrapOperationLocks(commands []*cobra.Command, appData func() string) {
	for _, command := range commands {
		run := command.RunE
		if run == nil {
			panic("operation command without RunE: " + command.Name())
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			target, locked := operationLockTarget(cmd, args, cmd == pullCmd || cmd == pushCmd)
			if !locked {
				return run(cmd, args)
			}
			timeout, err := operationLockTimeout(os.Getenv(lockTimeoutEnvVar))
			if err != nil {
				return err
			}
			release, err := operationlock.AcquireWithin(appData(), target, cmd.Name(), timeout)
			if err != nil {
				return err
			}
			defer release()
			if cmd == createCmd && commandInstanceName(cmd) != "" {
				image := strings.TrimSuffix(target, "/"+commandInstanceName(cmd))
				releaseImage, err := operationlock.AcquireSharedWithin(appData(), image, "clone image", timeout)
				if err != nil {
					return err
				}
				defer releaseImage()
			}

			if cmd == createCmd || cmd == buildCmd || cmd == pullCmd || cmd == pushCmd {
				imageTarget := target
				if name := commandInstanceName(cmd); name != "" {
					imageTarget = strings.TrimSuffix(imageTarget, "/"+name)
				}
				cacheLocks := filepath.Join(alchemy_build.GetDirectoriesInstance().CacheDir, "image-operation-locks")
				var releaseImage func()
				if cmd == createCmd {
					releaseImage, err = operationlock.AcquireSharedWithin(cacheLocks, imageTarget, "clone image", timeout)
				} else {
					releaseImage, err = operationlock.AcquireWithin(cacheLocks, imageTarget, cmd.Name()+" image", timeout)
				}
				if err != nil {
					return err
				}
				defer releaseImage()
			}
			return run(cmd, args)
		}
	}
}

// operationLockTarget returns the lock key: os/type/arch for one target, or "" for
// all targets. Transfers can resolve their target from registry metadata, so they
// lock all targets. locked is false for host-local or argument-less invocations.
func operationLockTarget(cmd *cobra.Command, args []string, transfer bool) (string, bool) {
	if len(args) == 0 || args[0] == "local" {
		return "", false
	}
	if args[0] == "all" || transfer {
		return "", true
	}
	typ, _ := cmd.Flags().GetString("type")
	arch, _ := cmd.Flags().GetString("arch")
	typ = alchemy_build.TargetType(args[0], typ)
	key := args[0] + "/" + typ + "/" + arch
	if name := commandInstanceName(cmd); name != "" {
		key += "/" + name
	}
	return key, true
}

func operationLockTimeout(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration such as 30s", lockTimeoutEnvVar)
	}
	return timeout, nil
}
