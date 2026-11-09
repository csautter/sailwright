package provision

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	runtimeassets "github.com/csautter/dev-alchemy"
)

const (
	localWindowsProvisionUserName        = "devalchemy_ansible"
	localWindowsBootstrapTimeout         = 2 * time.Minute
	localWindowsCleanupTimeout           = 2 * time.Minute
	localWindowsProvisionStatePathEnvVar = "DEV_ALCHEMY_LOCAL_WINDOWS_PROVISION_STATE_PATH"
	localWindowsProvisionUserEnvVar      = "DEV_ALCHEMY_LOCAL_WINDOWS_ANSIBLE_USER"
	localWindowsManagedUserScriptPath    = "scripts/windows/local-windows-managed-user.ps1"
)

func mustLoadLocalWindowsPowerShellAsset(path string) string {
	content, err := fs.ReadFile(runtimeassets.FS(), path)
	if err != nil {
		panic(fmt.Sprintf("load local windows powershell asset %q: %v", path, err))
	}

	return string(content)
}

func mustLoadLocalWindowsPowerShellAssets(paths ...string) string {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		parts = append(parts, mustLoadLocalWindowsPowerShellAsset(path))
	}

	return strings.Join(parts, "\n\n")
}

func createLocalWindowsProvisionStateFile(projectDir string) (string, error) {
	stateFile, err := os.CreateTemp(projectDir, ".local-windows-provision-state-*.json")
	if err != nil {
		return "", fmt.Errorf("failed to create secure local windows provision state file: %w", err)
	}
	statePath := stateFile.Name()
	if err := stateFile.Close(); err != nil {
		_ = os.Remove(statePath)
		return "", fmt.Errorf("failed to close secure local windows provision state file: %w", err)
	}

	return statePath, nil
}

func removeLocalWindowsProvisionStateFile(statePath string) error {
	removeErr := os.Remove(statePath)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("failed to remove secure local windows provision state file %q: %w", statePath, removeErr)
	}

	return nil
}

func runLocalWindowsPowerShellScript(projectDir string, script string, extraEnv []string, timeout time.Duration, logPrefix string) (string, error) {
	elevatedScriptPath, outputPath, cleanupFiles, err := writeElevatedLocalWindowsPowerShellArtifacts(projectDir, script, extraEnv)
	if err != nil {
		return "", err
	}
	defer cleanupFiles()

	stopStreaming := make(chan struct{})
	streamingDone := make(chan struct{})
	go streamLocalWindowsPowerShellOutput(outputPath, logPrefix, stopStreaming, streamingDone)

	output, runErr := runProvisionCommandWithCombinedOutputWithEnv(
		projectDir,
		timeout,
		"powershell.exe",
		[]string{
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy",
			"Bypass",
			"-Command",
			buildLocalWindowsElevationLauncherPowerShell(elevatedScriptPath, outputPath),
		},
		nil,
	)
	close(stopStreaming)
	<-streamingDone

	// #nosec G304 -- outputPath is created by writeElevatedLocalWindowsPowerShellArtifacts under the managed project directory.
	scriptOutputBytes, readErr := os.ReadFile(outputPath)
	scriptOutput := strings.TrimSpace(decodeLocalWindowsPowerShellOutput(scriptOutputBytes))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		if runErr != nil {
			return "", fmt.Errorf("failed to read elevated local windows provisioning output from %q: %w (launcher output: %s; launcher error: %v)", outputPath, readErr, strings.TrimSpace(output), runErr)
		}
		return "", fmt.Errorf("failed to read elevated local windows provisioning output from %q: %w", outputPath, readErr)
	}

	if runErr != nil {
		combinedOutput := strings.TrimSpace(strings.Join(filterNonEmptyStrings(scriptOutput, strings.TrimSpace(output)), "\n"))
		if combinedOutput == "" {
			combinedOutput = "no output captured; the Windows UAC prompt may have been cancelled"
		}
		return combinedOutput, runErr
	}

	return scriptOutput, nil
}

func writeElevatedLocalWindowsPowerShellArtifacts(projectDir string, script string, extraEnv []string) (string, string, func(), error) {
	elevatedScriptFile, err := os.CreateTemp(projectDir, ".local-windows-provision-elevated-*.ps1")
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create elevated local windows provisioning script: %w", err)
	}
	elevatedScriptPath := elevatedScriptFile.Name()
	if err := elevatedScriptFile.Close(); err != nil {
		_ = os.Remove(elevatedScriptPath)
		return "", "", nil, fmt.Errorf("failed to close elevated local windows provisioning script %q: %w", elevatedScriptPath, err)
	}

	outputFile, err := os.CreateTemp(projectDir, ".local-windows-provision-output-*.log")
	if err != nil {
		_ = os.Remove(elevatedScriptPath)
		return "", "", nil, fmt.Errorf("failed to create elevated local windows provisioning output file: %w", err)
	}
	outputPath := outputFile.Name()
	if err := outputFile.Close(); err != nil {
		_ = os.Remove(elevatedScriptPath)
		_ = os.Remove(outputPath)
		return "", "", nil, fmt.Errorf("failed to close elevated local windows provisioning output file %q: %w", outputPath, err)
	}

	scriptContent := buildElevatedLocalWindowsPowerShellScript(script, extraEnv, outputPath)
	if err := os.WriteFile(elevatedScriptPath, []byte(scriptContent), 0o600); err != nil {
		_ = os.Remove(elevatedScriptPath)
		_ = os.Remove(outputPath)
		return "", "", nil, fmt.Errorf("failed to write elevated local windows provisioning script %q: %w", elevatedScriptPath, err)
	}

	cleanupFiles := func() {
		_ = os.Remove(elevatedScriptPath)
		_ = os.Remove(outputPath)
	}

	return elevatedScriptPath, outputPath, cleanupFiles, nil
}

func buildElevatedLocalWindowsPowerShellScript(script string, extraEnv []string, outputPath string) string {
	var builder strings.Builder
	builder.WriteString("$ErrorActionPreference = 'Stop'\n\n")
	builder.WriteString(fmt.Sprintf("$outputPath = '%s'\n", escapePowerShellSingleQuotedString(outputPath)))
	builder.WriteString("if (Test-Path -Path $outputPath) {\n")
	builder.WriteString("    Remove-Item -Path $outputPath -Force\n")
	builder.WriteString("}\n\n")
	for _, entry := range extraEnv {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		builder.WriteString(fmt.Sprintf("$env:%s = '%s'\n", key, escapePowerShellSingleQuotedString(value)))
	}
	if len(extraEnv) > 0 {
		builder.WriteString("\n")
	}
	builder.WriteString("try {\n")
	builder.WriteString("    & {\n")
	builder.WriteString(script)
	builder.WriteString("\n    } *>> $outputPath\n")
	builder.WriteString("    exit $LASTEXITCODE\n")
	builder.WriteString("} catch {\n")
	builder.WriteString("    ($_ | Out-String) | Add-Content -Path $outputPath -Encoding Ascii\n")
	builder.WriteString("    exit 1\n")
	builder.WriteString("}\n")

	return builder.String()
}

func buildLocalWindowsElevationLauncherPowerShell(elevatedScriptPath string, outputPath string) string {
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'

$elevatedScriptPath = '%s'
$outputPath = '%s'

try {
    $windowsIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $windowsPrincipal = New-Object Security.Principal.WindowsPrincipal($windowsIdentity)
    $isElevated = $windowsPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

    $argumentList = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%s"'
    if ($isElevated) {
        & 'powershell.exe' -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $elevatedScriptPath
        exit $LASTEXITCODE
    }

    $process = Start-Process -FilePath 'powershell.exe' -ArgumentList $argumentList -Verb RunAs -WindowStyle Hidden -Wait -PassThru
    exit $process.ExitCode
} catch {
    ($_ | Out-String) | Add-Content -Path $outputPath -Encoding UTF8
    exit 1
}
`, escapePowerShellSingleQuotedString(elevatedScriptPath), escapePowerShellSingleQuotedString(outputPath), escapePowerShellDoubleQuotedArgument(elevatedScriptPath))
}

func escapePowerShellSingleQuotedString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func escapePowerShellDoubleQuotedArgument(value string) string {
	return strings.ReplaceAll(filepath.Clean(value), `"`, `""`)
}

func filterNonEmptyStrings(values ...string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func streamLocalWindowsPowerShellOutput(outputPath string, logPrefix string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	printedLength := 0
	pendingLine := ""
	flush := func() {
		// #nosec G304 -- outputPath is created by writeElevatedLocalWindowsPowerShellArtifacts under the managed project directory.
		content, err := os.ReadFile(outputPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return
			}
			return
		}

		decoded := decodeLocalWindowsPowerShellOutput(content)
		if len(decoded) <= printedLength {
			return
		}

		pendingLine = logLocalWindowsPowerShellOutputChunk(logPrefix, decoded[printedLength:], pendingLine, false)
		printedLength = len(decoded)
	}

	for {
		select {
		case <-stop:
			flush()
			logLocalWindowsPowerShellOutputChunk(logPrefix, "", pendingLine, true)
			return
		case <-ticker.C:
			flush()
		}
	}
}

func logLocalWindowsPowerShellOutputChunk(logPrefix string, chunk string, pendingLine string, flushPartial bool) string {
	pendingLine += strings.ReplaceAll(chunk, "\r\n", "\n")
	pendingLine = strings.ReplaceAll(pendingLine, "\r", "\n")

	for {
		newlineIndex := strings.IndexByte(pendingLine, '\n')
		if newlineIndex < 0 {
			break
		}

		line := strings.TrimSpace(pendingLine[:newlineIndex])
		if line != "" {
			log.Printf("%s powershell: %s", logPrefix, line)
		}
		pendingLine = pendingLine[newlineIndex+1:]
	}

	if flushPartial {
		line := strings.TrimSpace(pendingLine)
		if line != "" {
			log.Printf("%s powershell: %s", logPrefix, line)
		}
		return ""
	}

	return pendingLine
}

func decodeLocalWindowsPowerShellOutput(content []byte) string {
	if len(content) == 0 {
		return ""
	}

	if len(content) >= 2 {
		switch {
		case content[0] == 0xFF && content[1] == 0xFE:
			return decodeUTF16LittleEndian(content[2:])
		case content[0] == 0xFE && content[1] == 0xFF:
			return decodeUTF16BigEndian(content[2:])
		}
	}
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		return string(content[3:])
	}

	if looksLikeUTF16LittleEndian(content) {
		return decodeUTF16LittleEndian(content)
	}
	if looksLikeUTF16BigEndian(content) {
		return decodeUTF16BigEndian(content)
	}

	return string(content)
}

func looksLikeUTF16LittleEndian(content []byte) bool {
	if len(content) < 4 || len(content)%2 != 0 {
		return false
	}

	zeroCount := 0
	sampleCount := 0
	for index := 1; index < len(content) && sampleCount < 16; index += 2 {
		sampleCount++
		if content[index] == 0 {
			zeroCount++
		}
	}

	return sampleCount > 0 && zeroCount*2 >= sampleCount
}

func looksLikeUTF16BigEndian(content []byte) bool {
	if len(content) < 4 || len(content)%2 != 0 {
		return false
	}

	zeroCount := 0
	sampleCount := 0
	for index := 0; index < len(content) && sampleCount < 16; index += 2 {
		sampleCount++
		if content[index] == 0 {
			zeroCount++
		}
	}

	return sampleCount > 0 && zeroCount*2 >= sampleCount
}

func decodeUTF16LittleEndian(content []byte) string {
	if len(content)%2 != 0 {
		content = content[:len(content)-1]
	}
	runes := make([]rune, 0, len(content)/2)
	for index := 0; index+1 < len(content); index += 2 {
		runes = append(runes, rune(uint16(content[index])|uint16(content[index+1])<<8))
	}
	return string(runes)
}

func decodeUTF16BigEndian(content []byte) string {
	if len(content)%2 != 0 {
		content = content[:len(content)-1]
	}
	runes := make([]rune, 0, len(content)/2)
	for index := 0; index+1 < len(content); index += 2 {
		runes = append(runes, rune(uint16(content[index])<<8|uint16(content[index+1])))
	}
	return string(runes)
}

func generateSecureLocalWindowsProvisionPassword() (string, error) {
	const lowercase = "abcdefghijklmnopqrstuvwxyz"
	const uppercase = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const digits = "0123456789"
	// No braces: Ansible templates extra vars, so "{{" or "{%" in the
	// password would be read as Jinja.
	const special = "!@#$%^&*()-_=+[]"
	const all = lowercase + uppercase + digits + special

	requiredSets := []string{lowercase, uppercase, digits, special}
	passwordRunes := make([]byte, 0, 24)

	for _, charset := range requiredSets {
		index, err := randomInt(len(charset))
		if err != nil {
			return "", err
		}
		passwordRunes = append(passwordRunes, charset[index])
	}
	for len(passwordRunes) < 24 {
		index, err := randomInt(len(all))
		if err != nil {
			return "", err
		}
		passwordRunes = append(passwordRunes, all[index])
	}
	for index := len(passwordRunes) - 1; index > 0; index-- {
		swapIndex, err := randomInt(index + 1)
		if err != nil {
			return "", err
		}
		passwordRunes[index], passwordRunes[swapIndex] = passwordRunes[swapIndex], passwordRunes[index]
	}

	return string(passwordRunes), nil
}

func randomInt(max int) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("max must be greater than zero")
	}

	var randomByte [1]byte
	limit := 256 - (256 % max)
	for {
		if _, err := rand.Read(randomByte[:]); err != nil {
			return 0, err
		}
		if int(randomByte[0]) >= limit {
			continue
		}
		return int(randomByte[0]) % max, nil
	}
}
