package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// listCommands are the sailwright commands that offer `<command> list --json`.
var listCommands = []string{"build", "create", "start", "stop", "destroy", "provision", "pull", "push"}

// Target is one entry of `<command> list --json` reported by the pinned binary.
type Target struct {
	OS     string            `json:"os"`
	Type   string            `json:"type"`
	Arch   string            `json:"arch"`
	Engine string            `json:"engine"`
	Fields map[string]string `json:"fields,omitempty"`
}

type targetListing struct {
	SchemaVersion int      `json:"schema_version"`
	Targets       []Target `json:"targets"`
}

// TargetCatalog caches the valid targets per command, as reported by the pinned binary.
// The gateway never imports sailwright internals, so the enums always match the
// version under test.
type TargetCatalog struct {
	mu        sync.RWMutex
	byCommand map[string][]Target
	errors    map[string]string
	refreshed time.Time
}

// NewTargetCatalog returns an empty catalog.
func NewTargetCatalog() *TargetCatalog {
	return &TargetCatalog{byCommand: map[string][]Target{}, errors: map[string]string{}}
}

// Set stores the listing for one command.
func (c *TargetCatalog) Set(command string, targets []Target, listErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byCommand[command] = targets
	if listErr != nil {
		c.errors[command] = listErr.Error()
	} else {
		delete(c.errors, command)
	}
	c.refreshed = time.Now()
}

// Targets returns the cached targets for a command.
func (c *TargetCatalog) Targets(command string) []Target {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Target(nil), c.byCommand[command]...)
}

// Errors returns the last listing error per command.
func (c *TargetCatalog) Errors() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.errors))
	for k, v := range c.errors {
		out[k] = v
	}
	return out
}

// Match returns the targets of command that match the requested identity. Empty
// type or engine match any value.
func (c *TargetCatalog) Match(command string, osName, osType, arch, engine string) []Target {
	var matches []Target
	for _, target := range c.Targets(command) {
		if target.OS != osName || target.Arch != arch {
			continue
		}
		if osType != "" && target.Type != "" && target.Type != osType {
			continue
		}
		if engine != "" && target.Engine != engine {
			continue
		}
		matches = append(matches, target)
	}
	return matches
}

// HasTypedTargets reports whether a command lists the OS with a type (e.g. ubuntu
// server/desktop), which means the CLI default type applies when none is given.
func (c *TargetCatalog) HasTypedTargets(command string, osName string) bool {
	for _, target := range c.Targets(command) {
		if target.OS == osName && target.Type != "" {
			return true
		}
	}
	return false
}

// Summary returns the distinct os/type/arch/engine values per command.
func (c *TargetCatalog) Summary() map[string][]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string][]string{}
	for command, targets := range c.byCommand {
		seen := map[string]bool{}
		for _, target := range targets {
			id := strings.Join([]string{target.OS, target.Type, target.Arch, target.Engine}, "/")
			if !seen[id] {
				seen[id] = true
				out[command] = append(out[command], id)
			}
		}
		sort.Strings(out[command])
	}
	return out
}

// ParseTargetListing extracts the JSON listing from CLI output. Diagnostic lines the
// CLI may print before the document are ignored.
func ParseTargetListing(output []byte) ([]Target, error) {
	lines := bytes.Split(output, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if !bytes.HasPrefix(line, []byte(`{"schema_version"`)) {
			continue
		}
		var listing targetListing
		if err := json.Unmarshal(line, &listing); err != nil {
			return nil, fmt.Errorf("decode listing: %w", err)
		}
		if listing.SchemaVersion != 1 {
			return nil, fmt.Errorf("unsupported listing schema_version %d", listing.SchemaVersion)
		}
		for _, target := range listing.Targets {
			for _, value := range []string{target.OS, target.Type, target.Arch, target.Engine} {
				if value != "" && !identifierPattern.MatchString(value) {
					return nil, fmt.Errorf("listing contains an invalid identifier %q", value)
				}
			}
		}
		return listing.Targets, nil
	}
	return nil, errors.New("no JSON listing found in output; is the pinned sailwright binary new enough to support `list --json`?")
}
