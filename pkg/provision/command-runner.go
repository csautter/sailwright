package provision

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	scannerInitialBufferSize = 64 * 1024
	scannerMaxBufferSize     = 1024 * 1024
)

var (
	ansiblePasswordJSONRegex     = regexp.MustCompile(`(?i)(\"ansible_password\"\s*:\s*\")[^\"]*(\")`) // #nosec G101 -- redaction pattern for log sanitization, not a credential.
	ansiblePasswordKeyValueRegex = regexp.MustCompile(`(?i)(ansible_password=)\S+`)                    // #nosec G101 -- redaction pattern for log sanitization, not a credential.
	urlUserInfoRegex             = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@]+@)`)
)

func runCommandWithStreamingLogs(workingDir string, timeout time.Duration, executable string, args []string, logPrefix string) error {
	return runCommandWithStreamingLogsWithEnv(workingDir, timeout, executable, args, nil, logPrefix)
}

func runCommandWithStreamingLogsWithEnv(workingDir string, timeout time.Duration, executable string, args []string, extraEnv []string, logPrefix string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// #nosec G204,G702 -- callers provide explicit executables and argv slices; no shell interpretation occurs.
	cmd := exec.CommandContext(ctx, executable, args...)
	sanitizedArgs := sanitizeCommandArgsForLogs(args)
	cmd.Dir = workingDir
	cmd.Env = commandEnv(executable, extraEnv)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout for %q: %w", executable, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr for %q: %w", executable, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command %q %v: %w", executable, sanitizedArgs, err)
	}

	var streamsWG sync.WaitGroup

	streamOutput := func(output io.Reader, streamName string) {
		defer streamsWG.Done()
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, scannerInitialBufferSize), scannerMaxBufferSize)
		for scanner.Scan() {
			log.Printf("%s %s: %s", logPrefix, streamName, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Printf("%s %s scanner error: %v", logPrefix, streamName, err)
		}
	}

	streamsWG.Add(2)
	go streamOutput(stdout, "stdout")
	go streamOutput(stderr, "stderr")

	streamsDone := make(chan struct{})
	go func() {
		streamsWG.Wait()
		close(streamsDone)
	}()

	select {
	case <-streamsDone:
		err := cmd.Wait()
		if err != nil {
			return fmt.Errorf("command failed (%s %v): %w", executable, sanitizedArgs, err)
		}
		log.Printf("Command finished successfully: %s %v", executable, sanitizedArgs)
		return nil
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-streamsDone
		_ = cmd.Wait()
		return fmt.Errorf("command terminated due to timeout or interruption (%s %v): %w", executable, sanitizedArgs, ctx.Err())
	}
}

func runCommandWithCombinedOutput(workingDir string, timeout time.Duration, executable string, args []string) (string, error) {
	return runCommandWithCombinedOutputWithEnv(workingDir, timeout, executable, args, nil)
}

func runCommandWithCombinedOutputWithEnv(workingDir string, timeout time.Duration, executable string, args []string, extraEnv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// #nosec G204,G702 -- callers provide explicit executables and argv slices; no shell interpretation occurs.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workingDir
	cmd.Env = commandEnv(executable, extraEnv)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command failed (%s %v): %w", executable, sanitizeCommandArgsForLogs(args), err)
	}

	return string(output), nil
}

// commandEnv returns the environment for a child process, or nil to inherit the
// current environment unchanged.
func commandEnv(executable string, extraEnv []string) []string {
	environ, changed := sanitizeWindowsPowerShellEnv(executable, os.Environ())
	if !changed && len(extraEnv) == 0 {
		return nil
	}

	return append(environ, extraEnv...)
}

// sanitizeWindowsPowerShellEnv removes PowerShell 7 module directories from
// PSModulePath when the child process is Windows PowerShell. When sailwright is
// started from pwsh (e.g. the default shell of GitHub Actions Windows runners),
// Windows PowerShell inherits pwsh's PSModulePath and loads incompatible
// PowerShell 7 core modules: Microsoft.PowerShell.Security then fails to load
// and the Cert: drive is missing. pwsh applies the same mitigation itself when
// it starts powershell.exe directly.
func sanitizeWindowsPowerShellEnv(executable string, environ []string) ([]string, bool) {
	if !isWindowsPowerShellExecutable(executable) {
		return environ, false
	}

	sanitized := make([]string, 0, len(environ))
	changed := false
	for _, entry := range environ {
		key, value, found := strings.Cut(entry, "=")
		if !found || !strings.EqualFold(key, "PSModulePath") {
			sanitized = append(sanitized, entry)
			continue
		}

		filtered := filterPowerShellCoreModulePaths(value)
		if filtered == value {
			sanitized = append(sanitized, entry)
			continue
		}

		changed = true
		if strings.Trim(filtered, ";") != "" {
			sanitized = append(sanitized, key+"="+filtered)
		}
	}

	return sanitized, changed
}

func isWindowsPowerShellExecutable(executable string) bool {
	base := strings.ToLower(executable[strings.LastIndexAny(executable, `\/`)+1:])
	return base == "powershell" || base == "powershell.exe"
}

func filterPowerShellCoreModulePaths(modulePath string) string {
	entries := strings.Split(modulePath, ";")
	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		if isPowerShellCoreModulePath(entry) {
			continue
		}
		kept = append(kept, entry)
	}

	return strings.Join(kept, ";")
}

// isPowerShellCoreModulePath reports whether a module directory belongs to
// PowerShell 7: <...>\PowerShell\Modules (all users / current user) or
// <...>\PowerShell\<version>\Modules ($PSHOME). Windows PowerShell paths use a
// WindowsPowerShell segment instead.
func isPowerShellCoreModulePath(path string) bool {
	segments := strings.FieldsFunc(path, func(r rune) bool { return r == '\\' || r == '/' })
	count := len(segments)
	if count < 2 || !strings.EqualFold(segments[count-1], "Modules") {
		return false
	}
	if strings.EqualFold(segments[count-2], "PowerShell") {
		return true
	}

	return count >= 3 &&
		strings.EqualFold(segments[count-3], "PowerShell") &&
		segments[count-2] != "" &&
		segments[count-2][0] >= '0' && segments[count-2][0] <= '9'
}

func sanitizeCommandArgsForLogs(args []string) []string {
	if len(args) == 0 {
		return nil
	}

	sanitized := make([]string, len(args))
	for index, arg := range args {
		redacted := ansiblePasswordJSONRegex.ReplaceAllString(arg, `${1}***REDACTED***${2}`)
		redacted = ansiblePasswordKeyValueRegex.ReplaceAllString(redacted, `${1}***REDACTED***`)
		redacted = urlUserInfoRegex.ReplaceAllString(redacted, `${1}***REDACTED***@`)
		sanitized[index] = redacted
	}
	return sanitized
}
