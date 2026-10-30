package gateway

import (
	"path"
	"sort"
	"strings"
)

// Risk flags shown to the human approver. They are hints for review, not a policy:
// everything outside the safe set needs approval regardless of its flags.
const (
	RiskHostModifying   = "HOST-MODIFYING"
	RiskHostCode        = "HOST-CODE"
	RiskPassthroughArgs = "PASSTHROUGH-ARGS"
	RiskExternalPaths   = "EXTERNAL-PATHS"
	RiskNetworkWrite    = "NETWORK-WRITE"
	RiskCredentials     = "CREDENTIALS"
	RiskAllTargets      = "ALL-TARGETS"
)

var credentialFlags = []string{"--password", "--password-stdin", "--access-token", "--refresh-token", "--username"}

// AssessRisk derives review hints from raw sailwright arguments. Absolute paths inside
// bundleRoot (an agent path, may be empty) are snapshotted and not counted as external.
func AssessRisk(args []string, bundleRoot string) []string {
	flags := map[string]bool{}
	if len(args) == 0 {
		return nil
	}
	command := args[0]
	positional := ""
	passthrough := false
	for i, arg := range args {
		if passthrough {
			continue
		}
		if arg == "--" {
			passthrough = true
			flags[RiskPassthroughArgs] = true
			continue
		}
		if i > 0 && positional == "" && !strings.HasPrefix(arg, "-") && !isFlagValue(args, i) {
			positional = arg
		}
		name := arg
		if eq := strings.Index(name, "="); eq >= 0 {
			name = name[:eq]
		}
		if containsString(credentialFlags, name) {
			flags[RiskCredentials] = true
		}
		if name == "--playbook" || name == "--inventory-path" {
			flags[RiskHostCode] = true
		}
		if looksLikeAbsolutePath(arg) && !insideAgentRoot(arg, bundleRoot) {
			flags[RiskExternalPaths] = true
		}
	}

	switch command {
	case "install":
		flags[RiskHostModifying] = true
	case "push":
		flags[RiskNetworkWrite] = true
	case "provision":
		if positional == "local" {
			flags[RiskHostCode] = true
			if !containsString(args, "--check") && !containsString(args, "--dry-run") {
				flags[RiskHostModifying] = true
			}
		}
		if flags[RiskPassthroughArgs] {
			flags[RiskHostCode] = true
		}
	}
	if positional == "all" {
		flags[RiskAllTargets] = true
	}

	var out []string
	for flag := range flags {
		out = append(out, flag)
	}
	sort.Strings(out)
	return out
}

// isFlagValue reports whether args[i] is the value of a preceding "--flag value".
func isFlagValue(args []string, i int) bool {
	if i == 0 {
		return false
	}
	previous := args[i-1]
	if !strings.HasPrefix(previous, "-") || strings.Contains(previous, "=") || previous == "--" {
		return false
	}
	switch previous {
	case "--check", "--dry-run", "--yes", "-y", "--headless", "--verbose", "-v", "--no-cache",
		"--force-winrm-uninstall", "--force-ssh-uninstall", "--plain-http", "--insecure-skip-tls-verify",
		"--password-stdin", "--no-docker-credentials", "--with-go", "--virtualbox", "--json",
		"--no-prebuilt", "--ask-become-pass", "-K":
		return false
	}
	return true
}

func insideAgentRoot(arg string, root string) bool {
	if root == "" {
		return false
	}
	value := arg
	if eq := strings.Index(value, "="); eq >= 0 && strings.HasPrefix(value, "-") {
		value = value[eq+1:]
	}
	value = path.Clean(strings.TrimPrefix(value, "@"))
	return value == root || strings.HasPrefix(value, root+"/")
}

func looksLikeAbsolutePath(arg string) bool {
	value := arg
	if eq := strings.Index(value, "="); eq >= 0 && strings.HasPrefix(value, "-") {
		value = value[eq+1:]
	}
	value = strings.TrimPrefix(value, "@")
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "~") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}
