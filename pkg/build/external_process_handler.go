package build

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/csautter/dev-alchemy/internal/winpsenv"
)

type RunProcessConfig struct {
	ExecutablePath     string
	Args               []string
	Env                []string
	WorkingDir         string
	Timeout            time.Duration
	DelayBeforeStart   time.Duration
	Context            context.Context
	FailOnError        bool
	Retries            int
	RetryInterval      time.Duration
	InterruptRetryChan chan bool
	Silent             *atomic.Bool
}

func RunExternalProcess(config RunProcessConfig) (context.Context, error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if config.Context != nil {
		ctx, cancel = context.WithTimeout(config.Context, config.Timeout)
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), config.Timeout)
	}
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)

	if config.DelayBeforeStart > 0 {
		if !isSilent(config.Silent) {
			log.Printf("Delaying start of process %s by %s", config.ExecutablePath, config.DelayBeforeStart)
		}
		select {
		case <-time.After(config.DelayBeforeStart):
			// Reset DelayBeforeStart to 0 after the delay
			config.DelayBeforeStart = 0
			// continue
		case <-config.InterruptRetryChan:
			if !isSilent(config.Silent) {
				log.Printf("Process %s start interrupted by retry interrupt channel", config.ExecutablePath)
			}
			return cancelledContext(), fmt.Errorf("process %q start interrupted", config.ExecutablePath)
		case sig := <-sigs:
			if !isSilent(config.Silent) {
				log.Printf("Process %s start interrupted by signal: %v", config.ExecutablePath, sig)
			}
			return ctx, fmt.Errorf("process %q start interrupted by signal: %v", config.ExecutablePath, sig)
		case <-ctx.Done():
			if !isSilent(config.Silent) {
				log.Printf("Process %s start cancelled due to timeout or interruption: %v", config.ExecutablePath, ctx.Err())
			}
			return ctx, fmt.Errorf("process %q start cancelled: %w", config.ExecutablePath, ctx.Err())
		}
	}

	if !isSilent(config.Silent) {
		log.Printf("Starting process: %s %s", config.ExecutablePath, strings.Join(config.Args, " "))
	}

	// #nosec G204 -- callers pass explicit executables and argv slices; no shell parsing occurs here.
	cmd := exec.CommandContext(ctx, config.ExecutablePath, config.Args...)
	configureCommandForCleanup(cmd)
	cmd.Dir = config.WorkingDir
	cmd.Env = winpsenv.CommandEnv(config.ExecutablePath, config.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to get stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("Failed to get stderr: %v", err)
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start command: %v", err)
	}
	processGroupID := commandProcessGroupID(cmd)

	done := make(chan error, 1)

	go func() {
		streamProcessOutput(stdout, config.ExecutablePath, "stdout", config.Silent)
	}()

	go func() {
		streamProcessOutput(stderr, config.ExecutablePath, "stderr", config.Silent)
	}()

	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			terminateProcessGroup(processGroupID, processCleanupGracePeriod)
			if config.FailOnError {
				log.Fatalf("Process %s failed: %v", config.ExecutablePath, err)
			}
			if !isSilent(config.Silent) {
				log.Printf("Process %s finished with error: %v", config.ExecutablePath, err)
			}
			return ctx, fmt.Errorf("process %q exited with error: %w", config.ExecutablePath, err)
		}
		if !isSilent(config.Silent) {
			log.Printf("Process %s finished successfully.", config.ExecutablePath)
		}
	case <-config.InterruptRetryChan:
		terminateProcessGroup(processGroupID, processCleanupGracePeriod)
		if !isSilent(config.Silent) {
			log.Printf("Process %s terminated due to retry interrupt channel", config.ExecutablePath)
		}
		return cancelledContext(), fmt.Errorf("process %q interrupted", config.ExecutablePath)
	case <-ctx.Done():
		// Kill the process if context is done (timeout or cancellation)
		terminateProcessGroup(processGroupID, processCleanupGracePeriod)
		if !isSilent(config.Silent) {
			log.Printf("Process %s terminated due to timeout or interruption: %v", config.ExecutablePath, ctx.Err())
		}
		return ctx, fmt.Errorf("process %q terminated: %w", config.ExecutablePath, ctx.Err())
	case sig := <-sigs:
		terminateProcessGroup(processGroupID, processCleanupGracePeriod)
		if !isSilent(config.Silent) {
			log.Printf("Process %s terminated due to signal: %v", config.ExecutablePath, sig)
		}
		return ctx, fmt.Errorf("process %q terminated by signal: %v", config.ExecutablePath, sig)
	}
	return ctx, nil
}

func streamProcessOutput(reader io.Reader, executablePath string, streamName string, silent *atomic.Bool) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if isSilent(silent) {
			continue
		}
		log.Printf("%s:%s:  %s", streamName, executablePath, scanner.Text())
	}
}

func isSilent(silent *atomic.Bool) bool {
	return silent != nil && silent.Load()
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func RunExternalProcessWithRetries(config RunProcessConfig) context.Context {
	var lastErr error
	startTime := time.Now()

	// Register a signal channel at the retry-loop level so that Ctrl+C
	// stops retrying immediately, even while sleeping between attempts.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)

	for attempt := 0; attempt <= config.Retries && time.Since(startTime) < config.Timeout; attempt++ {
		if attempt > 0 {
			if !isSilent(config.Silent) {
				log.Printf("Retrying process %s (attempt %d/%d) after error: %v", config.ExecutablePath, attempt, config.Retries, lastErr)
			}
			// Interruptible retry-interval sleep
			select {
			case sig := <-sigs:
				if !isSilent(config.Silent) {
					log.Printf("Retry loop for process %s interrupted by signal: %v", config.ExecutablePath, sig)
				}
				return cancelledContext()
			case <-config.InterruptRetryChan:
				if !isSilent(config.Silent) {
					log.Printf("Received interrupt signal while waiting to retry process %s", config.ExecutablePath)
				}
				return cancelledContext()
			case <-time.After(config.RetryInterval):
			}
		}

		// Check for a pending signal before starting the next attempt
		select {
		case sig := <-sigs:
			if !isSilent(config.Silent) {
				log.Printf("Retry loop for process %s interrupted by signal before attempt %d: %v", config.ExecutablePath, attempt, sig)
			}
			return cancelledContext()
		default:
		}
		if config.InterruptRetryChan != nil {
			select {
			case <-config.InterruptRetryChan:
				if !isSilent(config.Silent) {
					log.Printf("Received interrupt signal before starting process %s attempt %d", config.ExecutablePath, attempt)
				}
				return cancelledContext()
			default:
			}
		}

		ctx, err := RunExternalProcess(config)
		if err == nil {
			return ctx
		}
		if ctx != nil && ctx.Err() == context.Canceled {
			return ctx
		}
		lastErr = err

		// Check if we received an interrupt signal to stop retries
		if config.InterruptRetryChan != nil {
			select {
			case <-config.InterruptRetryChan:
				if !isSilent(config.Silent) {
					log.Printf("Received interrupt signal, stopping retries for process %s", config.ExecutablePath)
				}
				return ctx
			default:
				// No interrupt signal, continue
			}
		}

		// If RunExternalProcess itself was interrupted by a signal, the outer
		// sigs channel will also have received it — check and stop retrying.
		select {
		case sig := <-sigs:
			if !isSilent(config.Silent) {
				log.Printf("Retry loop for process %s stopping after signal: %v", config.ExecutablePath, sig)
			}
			return cancelledContext()
		default:
		}
	}
	if !isSilent(config.Silent) {
		log.Printf("Process %s failed after %d attempts: %v", config.ExecutablePath, config.Retries+1, lastErr)
	}
	return cancelledContext()
}

func RunBashScript(config RunProcessConfig) error {
	scriptPath := config.ExecutablePath
	config.ExecutablePath = "bash"
	config.Args = append([]string{scriptPath}, config.Args...)
	_, err := RunExternalProcess(config)
	return err
}

func RunPowerShellScript(config RunProcessConfig) error {
	scriptPath := config.ExecutablePath
	config.ExecutablePath = "powershell"
	config.Args = append([]string{"-File", scriptPath}, config.Args...)
	_, err := RunExternalProcess(config)
	return err
}

func RunCliCommand(workdir string, command string, args []string) ([]byte, error) {
	return RunCliCommandWithEnv(workdir, command, args, nil)
}

func RunCliCommandWithEnv(workdir string, command string, args []string, env []string) ([]byte, error) {
	// #nosec G204 -- this helper executes known CLI tools with explicit argv slices, not shell strings.
	cmd := exec.Command(command, args...)
	cmd.Dir = workdir
	cmd.Env = winpsenv.CommandEnv(command, env)
	log.Printf("Running command: %s %s", command, strings.Join(sanitizeCommandArgs(args), " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to run command %s: %v", command, err)
	}
	log.Printf("Command output: %s", sanitizeSensitiveText(string(output)))
	return output, err
}
