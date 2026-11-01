package provision

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/csautter/dev-alchemy/internal/winpsenv"
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
	cmd.Env = winpsenv.CommandEnv(executable, extraEnv)

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
	cmd.Env = winpsenv.CommandEnv(executable, extraEnv)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("command failed (%s %v): %w", executable, sanitizeCommandArgsForLogs(args), err)
	}

	return string(output), nil
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
