// Package runner executes deterministic sailwright E2E scenarios. Scenarios are YAML
// data; the runner maps their steps to an Executor, either the CLI directly or the
// sailwright-gateway over MCP. The runner knows what to test and what to expect; the
// gateway only decides how commands may run.
package runner

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Operations understood by every executor.
const (
	OpBuild          = "build"
	OpCreate         = "create"
	OpStart          = "start"
	OpStop           = "stop"
	OpDestroy        = "destroy"
	OpProvision      = "provision"
	OpPull           = "pull"
	OpProvisionLocal = "provision_local"
	OpList           = "list"
	OpCLI            = "cli"
)

// Requirements a scenario can declare.
const (
	// RequireTarget needs SAILWRIGHT_E2E_TARGET.
	RequireTarget = "target"
	// RequireHostChanges needs host approval (gateway) or SAILWRIGHT_E2E_ALLOW_HOST_CHANGES=1 (direct).
	RequireHostChanges = "host-changes"
)

var (
	knownOps          = map[string]bool{OpBuild: true, OpCreate: true, OpStart: true, OpStop: true, OpDestroy: true, OpProvision: true, OpPull: true, OpProvisionLocal: true, OpList: true, OpCLI: true}
	targetOps         = map[string]bool{OpBuild: true, OpCreate: true, OpStart: true, OpStop: true, OpDestroy: true, OpProvision: true, OpPull: true}
	knownRequirements = map[string]bool{RequireTarget: true, RequireHostChanges: true}
	knownHostOS       = map[string]bool{"linux": true, "darwin": true, "windows": true}
	knownListCommands = map[string]bool{"build": true, "create": true, "start": true, "stop": true, "destroy": true, "provision": true, "pull": true, "push": true}
	scenarioName      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	envReference      = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)
)

// Scenario is one deterministic test case.
type Scenario struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Requires    []string `yaml:"requires"`
	// HostOS limits the scenario to one host OS: linux, darwin or windows.
	HostOS string `yaml:"host_os"`
	// Targets turns the scenario into a matrix: the steps run once per target.
	Targets []string `yaml:"targets"`
	// Covers names a list command whose targets must all appear in Targets, so new
	// VM options cannot silently go untested.
	Covers string `yaml:"covers"`
	// Preflight steps check that the host is fit for the scenario before it changes
	// anything. If one fails, the steps and the always steps are skipped, so cleanup
	// never touches state the scenario did not create, such as an existing VM.
	Preflight []Step `yaml:"preflight"`
	Steps     []Step `yaml:"steps"`
	Always    []Step `yaml:"always"`

	File    string   `yaml:"-"`
	targets []Target `yaml:"-"`
}

// Step is one operation plus its expectations.
type Step struct {
	Name      string         `yaml:"name"`
	Op        string         `yaml:"op"`
	With      map[string]any `yaml:"with"`
	Expect    Expect         `yaml:"expect"`
	WhenEnv   string         `yaml:"when_env"`
	UnlessEnv string         `yaml:"unless_env"`
	Only      string         `yaml:"only"`
	// WhenListed runs the step only if the target appears in `<command> list`,
	// e.g. build is skipped for targets that use a public image.
	WhenListed string `yaml:"when_listed"`
}

// Expect describes what a step must produce. Without exit_code, 0 is expected.
type Expect struct {
	ExitCode       *int              `yaml:"exit_code"`
	OutputRegex    string            `yaml:"output_regex"`
	OutputNotRegex string            `yaml:"output_not_regex"`
	Recap          map[string]int    `yaml:"recap"`
	State          *StateExpectation `yaml:"state"`
	MaxDuration    string            `yaml:"max_duration"`
	maxDuration    time.Duration     `yaml:"-"`
	outputRegex    *regexp.Regexp    `yaml:"-"`
	outputNotRegex *regexp.Regexp    `yaml:"-"`
}

// StateExpectation checks a field of `<command> list --json` for the scenario target.
type StateExpectation struct {
	Command string `yaml:"command"`
	Field   string `yaml:"field"`
	Regex   string `yaml:"regex"`
	regex   *regexp.Regexp
}

// LoadScenarios reads every *.yaml file in dir, sorted by name.
func LoadScenarios(dir string) ([]Scenario, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var scenarios []Scenario
	seen := map[string]string{}
	for _, path := range paths {
		scenario, err := LoadScenario(path)
		if err != nil {
			return nil, err
		}
		if other, ok := seen[scenario.Name]; ok {
			return nil, fmt.Errorf("scenario %q is defined in %s and %s", scenario.Name, other, path)
		}
		seen[scenario.Name] = path
		scenarios = append(scenarios, scenario)
	}
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("no scenarios found in %s", dir)
	}
	return scenarios, nil
}

// LoadScenario reads and validates one scenario file. Unknown fields are errors.
func LoadScenario(path string) (Scenario, error) {
	// #nosec G304 -- scenario files are part of the repository.
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var scenario Scenario
	if err := decoder.Decode(&scenario); err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	scenario.File = path
	if err := scenario.validate(); err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	return scenario, nil
}

func (s *Scenario) validate() error {
	if !scenarioName.MatchString(s.Name) {
		return fmt.Errorf("invalid scenario name %q", s.Name)
	}
	if base := strings.TrimSuffix(filepath.Base(s.File), ".yaml"); base != s.Name {
		return fmt.Errorf("scenario name %q must match the file name %q", s.Name, base)
	}
	for _, requirement := range s.Requires {
		if !knownRequirements[requirement] {
			return fmt.Errorf("unknown requirement %q", requirement)
		}
	}
	if len(s.Steps) == 0 {
		return errors.New("a scenario needs at least one step")
	}
	usesTarget := false
	for i := range s.Preflight {
		if err := s.Preflight[i].validate(); err != nil {
			return fmt.Errorf("preflight step %d (%s): %w", i+1, s.Preflight[i].Name, err)
		}
		usesTarget = usesTarget || targetOps[s.Preflight[i].Op]
	}
	for i := range s.Steps {
		if err := s.Steps[i].validate(); err != nil {
			return fmt.Errorf("step %d (%s): %w", i+1, s.Steps[i].Name, err)
		}
		usesTarget = usesTarget || targetOps[s.Steps[i].Op]
	}
	for i := range s.Always {
		if err := s.Always[i].validate(); err != nil {
			return fmt.Errorf("always step %d (%s): %w", i+1, s.Always[i].Name, err)
		}
		usesTarget = usesTarget || targetOps[s.Always[i].Op]
	}
	if s.HostOS != "" && !knownHostOS[s.HostOS] {
		return fmt.Errorf("host_os must be linux, darwin or windows, not %q", s.HostOS)
	}
	if s.Covers != "" && !knownListCommands[s.Covers] {
		return fmt.Errorf("covers must name a list command, not %q", s.Covers)
	}
	if s.Covers != "" && len(s.Targets) == 0 {
		return errors.New("covers needs a targets matrix")
	}
	seenTargets := map[string]bool{}
	for _, value := range s.Targets {
		target, err := ParseTarget(value)
		if err != nil {
			return err
		}
		if seenTargets[target.String()] {
			return fmt.Errorf("target %s is listed twice", target)
		}
		seenTargets[target.String()] = true
		s.targets = append(s.targets, target)
	}
	if usesTarget && !s.Needs(RequireTarget) && len(s.Targets) == 0 {
		return errors.New(`scenarios with VM steps must declare "requires: [target]"`)
	}
	return nil
}

// Needs reports whether the scenario declares a requirement.
func (s Scenario) Needs(requirement string) bool {
	for _, value := range s.Requires {
		if value == requirement {
			return true
		}
	}
	return false
}

func (st *Step) validate() error {
	if strings.TrimSpace(st.Name) == "" {
		return errors.New("step name is required")
	}
	if !knownOps[st.Op] {
		return fmt.Errorf("unknown op %q", st.Op)
	}
	if st.Only != "" && st.Only != ExecutorDirect && st.Only != ExecutorGateway {
		return fmt.Errorf("only must be %q or %q", ExecutorDirect, ExecutorGateway)
	}
	if st.Op == OpCLI {
		if _, err := st.stringList("args"); err != nil {
			return err
		}
	}
	if st.WhenListed != "" && !knownListCommands[st.WhenListed] {
		return fmt.Errorf("when_listed must name a list command, not %q", st.WhenListed)
	}
	if st.Op == OpList {
		if command, _ := st.With["command"].(string); command == "" {
			return errors.New(`list needs "with: {command: ...}"`)
		}
	}
	var err error
	if st.Expect.maxDuration, err = parseOptionalDuration(st.Expect.MaxDuration); err != nil {
		return fmt.Errorf("max_duration: %w", err)
	}
	if st.Expect.OutputRegex != "" {
		if st.Expect.outputRegex, err = regexp.Compile(st.Expect.OutputRegex); err != nil {
			return fmt.Errorf("output_regex: %w", err)
		}
	}
	if st.Expect.OutputNotRegex != "" {
		if st.Expect.outputNotRegex, err = regexp.Compile(st.Expect.OutputNotRegex); err != nil {
			return fmt.Errorf("output_not_regex: %w", err)
		}
	}
	for key := range st.Expect.Recap {
		if !recapKeys[key] {
			return fmt.Errorf("unknown recap key %q", key)
		}
	}
	if state := st.Expect.State; state != nil {
		if state.Command == "" || state.Field == "" || state.Regex == "" {
			return errors.New("state needs command, field and regex")
		}
		if state.regex, err = regexp.Compile(state.Regex); err != nil {
			return fmt.Errorf("state regex: %w", err)
		}
	}
	return nil
}

func parseOptionalDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

// expectedExitCode returns the expected exit code (default 0).
func (e Expect) expectedExitCode() int {
	if e.ExitCode == nil {
		return 0
	}
	return *e.ExitCode
}

// Enabled reports whether the step runs for this executor and environment.
func (st Step) Enabled(executor string, getenv func(string) string) (bool, string) {
	if st.Only != "" && st.Only != executor {
		return false, "only for the " + st.Only + " executor"
	}
	if st.WhenEnv != "" && getenv(st.WhenEnv) == "" {
		return false, st.WhenEnv + " is not set"
	}
	if st.UnlessEnv != "" && getenv(st.UnlessEnv) != "" {
		return false, st.UnlessEnv + " is set"
	}
	return true, ""
}

// String returns a string parameter with ${SAILWRIGHT_E2E_*} references expanded.
func (st Step) String(key string, getenv func(string) string) string {
	value, _ := st.With[key].(string)
	return expandEnv(value, getenv)
}

// Bool returns a boolean parameter.
func (st Step) Bool(key string) bool {
	value, _ := st.With[key].(bool)
	return value
}

// Int returns an integer parameter and whether it was set.
func (st Step) Int(key string) (int, bool) {
	value, ok := st.With[key].(int)
	return value, ok
}

// Args returns the cli args with ${SAILWRIGHT_E2E_*} references expanded.
func (st Step) Args(getenv func(string) string) []string {
	values, _ := st.stringList("args")
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = expandEnv(value, getenv)
	}
	return out
}

func (st Step) stringList(key string) ([]string, error) {
	raw, ok := st.With[key].([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("%q must be a non-empty list of strings", key)
	}
	out := make([]string, len(raw))
	for i, value := range raw {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%q must only contain strings", key)
		}
		out[i] = text
	}
	return out, nil
}

// expandEnv expands ${SAILWRIGHT_E2E_*} references only; anything else stays literal.
func expandEnv(value string, getenv func(string) string) string {
	return envReference.ReplaceAllStringFunc(value, func(match string) string {
		name := envReference.FindStringSubmatch(match)[1]
		if !strings.HasPrefix(name, "SAILWRIGHT_E2E_") {
			return match
		}
		return getenv(name)
	})
}

// EnvReferences returns the environment variables a scenario reads through when_env,
// unless_env or ${SAILWRIGHT_E2E_*} references in step parameters.
func (s Scenario) EnvReferences() []string {
	var names []string
	collect := func(value string) {
		for _, match := range envReference.FindAllStringSubmatch(value, -1) {
			names = append(names, match[1])
		}
	}
	for _, step := range append(append(append([]Step{}, s.Preflight...), s.Steps...), s.Always...) {
		if step.WhenEnv != "" {
			names = append(names, step.WhenEnv)
		}
		if step.UnlessEnv != "" {
			names = append(names, step.UnlessEnv)
		}
		for _, value := range step.With {
			switch typed := value.(type) {
			case string:
				collect(typed)
			case []any:
				for _, item := range typed {
					if text, ok := item.(string); ok {
						collect(text)
					}
				}
			}
		}
	}
	return names
}

// Matrix returns the parsed targets matrix (empty for single-target scenarios).
func (s Scenario) Matrix() []Target {
	return append([]Target(nil), s.targets...)
}
