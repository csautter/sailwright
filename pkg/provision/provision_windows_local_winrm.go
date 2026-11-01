package provision

import (
	"fmt"
	"strings"
	"time"
)

const (
	localWindowsWinRMInventoryPath   = "./inventory/localhost_windows_winrm.yml"
	localWindowsWinRMInventoryTarget = "windows_host"
	localWindowsWinRMHTTPSPort       = "5986"

	// The first WinRM shell for the freshly created local Ansible account has
	// to create its Windows user profile, which can exceed Ansible's default
	// 20s operation timeout on slow hosts (e.g. GitHub-hosted runners) and
	// surfaces as an UNREACHABLE "basic:" error. The read timeout must stay
	// above the operation timeout.
	localWindowsWinRMOperationTimeoutSec = "120"
	localWindowsWinRMReadTimeoutSec      = "130"

	localWindowsProvisionPasswordEnvVar   = "DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_PASSWORD" // #nosec G101 -- environment variable name, not an embedded credential.
	localWindowsForceWinRMUninstallEnvVar = "DEV_ALCHEMY_LOCAL_WINDOWS_FORCE_WINRM_UNINSTALL"
	localWindowsWinRMBootstrapLogPrefix   = "local:windows:winrm:bootstrap"
	localWindowsWinRMCleanupLogPrefix     = "local:windows:winrm:cleanup"
	localWindowsWinRMBootstrapScriptPath  = "scripts/windows/local-windows-provision-winrm-bootstrap.ps1"
	localWindowsWinRMCleanupScriptPath    = "scripts/windows/local-windows-provision-winrm-cleanup.ps1"
)

var setupLocalWindowsWinRMProvisionSessionFunc = setupLocalWindowsWinRMProvisionSession
var cleanupLocalWindowsWinRMProvisionSessionFunc = cleanupLocalWindowsWinRMProvisionSession
var localWindowsWinRMProvisionBootstrapPowerShell = mustLoadLocalWindowsPowerShellAssets(localWindowsManagedUserScriptPath, localWindowsWinRMBootstrapScriptPath)
var localWindowsWinRMProvisionCleanupPowerShell = mustLoadLocalWindowsPowerShellAssets(localWindowsManagedUserScriptPath, localWindowsWinRMCleanupScriptPath)

type localWindowsWinRMProvisionSession struct {
	ConnectionConfig windowsAnsibleConnectionConfig
	StatePath        string
}

func runLocalWindowsWinRMProvision(projectDir string, options ProvisionOptions) error {
	return runLocalWindowsProvisionSession(projectDir, options, localWindowsProvisionSessionRunner[localWindowsWinRMProvisionSession]{
		setup:   setupLocalWindowsWinRMProvisionSessionFunc,
		cleanup: cleanupLocalWindowsWinRMProvisionSessionFunc,
		buildArgs: func(projectDir string, session localWindowsWinRMProvisionSession, options ProvisionOptions) ([]string, func() error, error) {
			inventoryPath, inventoryTarget := resolveStaticInventoryPathAndTarget(
				localWindowsWinRMInventoryPath,
				localWindowsWinRMInventoryTarget,
				options,
			)

			return buildWindowsStaticInventoryProvisionArgs(
				projectDir,
				inventoryPath,
				inventoryTarget,
				session.ConnectionConfig,
				options,
			)
		},
		buildArgsError: func(err error, cleanupErr error) error {
			return formatLocalWindowsProvisionStepError(
				"failed to build ansible arguments for secure local windows WinRM provision",
				err,
				cleanupErr,
				"WinRM",
			)
		},
		provisionResult: func(runErr error, argsCleanupErr error, cleanupErr error) error {
			return formatLocalWindowsProvisionOutcome("winrm", "WinRM", runErr, argsCleanupErr, cleanupErr)
		},
		ansibleLogPrefix: "local:windows:winrm:provision",
		runTimeout:       90 * time.Minute,
	})
}

func buildWindowsStaticInventoryProvisionArgs(projectDir string, inventoryPath string, inventoryTarget string, connectionConfig windowsAnsibleConnectionConfig, options ProvisionOptions) ([]string, func() error, error) {
	extraVars, err := buildWindowsProvisionExtraVars(connectionConfig)
	if err != nil {
		return nil, nil, err
	}

	return buildStaticInventoryProvisionArgsWithExtraVars(projectDir, inventoryPath, inventoryTarget, extraVars, options)
}

func buildLocalWindowsWinRMProvisionScriptEnv(statePath string, password string, options ProvisionOptions) []string {
	env := []string{
		localWindowsProvisionStatePathEnvVar + "=" + statePath,
		localWindowsProvisionUserEnvVar + "=" + localWindowsProvisionUserName,
		localWindowsForceWinRMUninstallEnvVar + "=" + fmt.Sprintf("%t", options.LocalWindowsForceWinRMUninstall),
	}
	if password == "" {
		return env
	}

	return append(env, localWindowsProvisionPasswordEnvVar+"="+password)
}

func setupLocalWindowsWinRMProvisionSession(projectDir string, options ProvisionOptions) (localWindowsWinRMProvisionSession, error) {
	password, err := generateSecureLocalWindowsProvisionPassword()
	if err != nil {
		return localWindowsWinRMProvisionSession{}, fmt.Errorf("failed to generate secure local windows provision password: %w", err)
	}

	statePath, err := createLocalWindowsProvisionStateFile(projectDir)
	if err != nil {
		return localWindowsWinRMProvisionSession{}, err
	}

	output, runErr := runLocalWindowsPowerShellScript(
		projectDir,
		localWindowsWinRMProvisionBootstrapPowerShell,
		buildLocalWindowsWinRMProvisionScriptEnv(statePath, password, options),
		localWindowsBootstrapTimeout,
		localWindowsWinRMBootstrapLogPrefix,
	)
	if runErr != nil {
		cleanupErr := cleanupLocalWindowsWinRMProvisionSession(projectDir, localWindowsWinRMProvisionSession{StatePath: statePath}, options)
		if cleanupErr != nil {
			return localWindowsWinRMProvisionSession{}, fmt.Errorf("failed to securely bootstrap local WinRM access: %w; output: %s (also failed to restore secure WinRM state: %v)", runErr, strings.TrimSpace(output), cleanupErr)
		}
		return localWindowsWinRMProvisionSession{}, fmt.Errorf("failed to securely bootstrap local WinRM access: %w; output: %s", runErr, strings.TrimSpace(output))
	}

	return localWindowsWinRMProvisionSession{
		ConnectionConfig: windowsAnsibleConnectionConfig{
			User:                 localWindowsProvisionUserName,
			Password:             password,
			Connection:           "winrm",
			WinrmTransport:       "basic",
			Port:                 localWindowsWinRMHTTPSPort,
			WinrmScheme:          "https",
			ServerCertValidation: "ignore",

			WinrmOperationTimeoutSec: localWindowsWinRMOperationTimeoutSec,
			WinrmReadTimeoutSec:      localWindowsWinRMReadTimeoutSec,
		},
		StatePath: statePath,
	}, nil
}

func cleanupLocalWindowsWinRMProvisionSession(projectDir string, session localWindowsWinRMProvisionSession, options ProvisionOptions) error {
	if session.StatePath == "" {
		return nil
	}

	output, runErr := runLocalWindowsPowerShellScript(
		projectDir,
		localWindowsWinRMProvisionCleanupPowerShell,
		buildLocalWindowsWinRMProvisionScriptEnv(session.StatePath, "", options),
		localWindowsCleanupTimeout,
		localWindowsWinRMCleanupLogPrefix,
	)
	removeErr := removeLocalWindowsProvisionStateFile(session.StatePath)
	if removeErr != nil {
		if runErr != nil {
			return fmt.Errorf("failed to restore secure WinRM state: %w; output: %s (also failed to remove secure local windows provision state file %q: %v)", runErr, strings.TrimSpace(output), session.StatePath, removeErr)
		}
		return removeErr
	}
	if runErr != nil {
		return fmt.Errorf("failed to restore secure WinRM state: %w; output: %s", runErr, strings.TrimSpace(output))
	}

	return nil
}
