package gateway

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// This file is the only place where sailwright argv is built or classified.
//
// Typed tools build an Operation and render it with Argv. Free-form run_cli calls are
// parsed with ParseSafe: if the arguments map onto a safe Operation, the gateway runs
// the canonical argv rendered from that Operation (never the raw input). Anything
// that does not parse is not rejected; it needs host approval instead.

// OpKind identifies an operation of the safe set.
type OpKind string

const (
	OpList      OpKind = "list"
	OpHelp      OpKind = "help"
	OpBuild     OpKind = "build"
	OpCreate    OpKind = "create"
	OpStart     OpKind = "start"
	OpStop      OpKind = "stop"
	OpDestroy   OpKind = "destroy"
	OpProvision OpKind = "provision"
	OpPull      OpKind = "pull"
)

const (
	defaultArch      = "amd64"
	defaultOSType    = "server"
	maxVerbosity     = 4
	maxArgs          = 64
	maxArgLength     = 1024
	maxReferenceSize = 255
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	playbookPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	// OCI reference: registry[:port]/repository[:tag|@sha256:digest], lower-case repository.
	ociReferencePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?(/[a-z0-9]+([._-][a-z0-9]+)*)+(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[a-f0-9]{64})?$`)
)

// vmCommands accept a single VM target.
var vmCommands = map[OpKind]bool{OpBuild: true, OpCreate: true, OpStart: true, OpStop: true, OpDestroy: true, OpProvision: true}

// Operation is a fully validated safe-set operation.
type Operation struct {
	Kind     OpKind
	Command  string   // for OpList: the listed command
	HelpPath []string // for OpHelp: the command path, e.g. ["provision"]
	OS       string
	Type     string
	Arch     string
	Engine   string
	Check    bool
	// Verbosity is the Ansible verbosity; -1 keeps the CLI default.
	Verbosity int
	// Playbook is the name of an embedded playbook (without .yml); empty keeps the default.
	Playbook string
	Headless bool
	Verbose  bool
	NoCache  bool
	Ref      string
}

// GrammarContext carries everything the grammar needs to validate an operation.
type GrammarContext struct {
	Catalog       *TargetCatalog
	PlaybookDir   string
	Playbooks     []string
	PullAllowlist []string
}

// Argv renders the canonical sailwright argv for a validated operation.
func (o Operation) Argv(ctx GrammarContext) []string {
	switch o.Kind {
	case OpList:
		return []string{o.Command, "list", "--json"}
	case OpHelp:
		return append(append([]string{}, o.HelpPath...), "--help")
	case OpPull:
		argv := []string{"pull", o.Ref, "--os", o.OS, "--arch", o.Arch}
		if o.Type != "" {
			argv = append(argv, "--type", o.Type)
		}
		if o.Engine != "" {
			argv = append(argv, "--engine", o.Engine)
		}
		return append(argv, "--yes", "--no-docker-credentials")
	}

	argv := []string{string(o.Kind), o.OS, "--arch", o.Arch}
	if o.Type != "" {
		argv = append(argv, "--type", o.Type)
	}
	switch o.Kind {
	case OpBuild:
		if o.Engine != "" {
			argv = append(argv, "--engine", o.Engine)
		}
		if o.Headless {
			argv = append(argv, "--headless")
		}
		if o.Verbose {
			argv = append(argv, "--verbose")
		}
		if o.NoCache {
			argv = append(argv, "--no-cache")
		}
	case OpProvision:
		if o.Check {
			argv = append(argv, "--check")
		}
		if o.Verbosity >= 0 {
			argv = append(argv, "--verbosity", strconv.Itoa(o.Verbosity))
		}
		if o.Playbook != "" {
			argv = append(argv, "--playbook", embeddedPlaybookPath(ctx.PlaybookDir, o.Playbook))
		}
	}
	return argv
}

func embeddedPlaybookPath(playbookDir string, name string) string {
	return filepath.Join(playbookDir, name+".yml")
}

// Validate checks an operation against the identifier rules and the target catalog
// and fills CLI defaults (arch, type) so the canonical argv is explicit.
func (o *Operation) Validate(ctx GrammarContext) error {
	switch o.Kind {
	case OpList:
		if !containsString(listCommands, o.Command) {
			return fmt.Errorf("%q has no list subcommand", o.Command)
		}
		return nil
	case OpHelp:
		return validateHelpPath(o.HelpPath)
	case OpPull:
		if err := validatePullReference(o.Ref, ctx.PullAllowlist); err != nil {
			return err
		}
	default:
		if !vmCommands[o.Kind] {
			return fmt.Errorf("operation %q is not in the safe set", o.Kind)
		}
	}

	if o.OS == "all" {
		return errors.New(`"all" targets are not in the safe set`)
	}
	if o.OS == "local" {
		return errors.New("local provisioning changes the host and needs approval")
	}
	if o.Arch == "" {
		o.Arch = defaultArch
	}
	for name, value := range map[string]string{"os": o.OS, "type": o.Type, "arch": o.Arch, "engine": o.Engine} {
		if value != "" && !identifierPattern.MatchString(value) {
			return fmt.Errorf("invalid %s %q", name, value)
		}
	}
	if o.OS == "" {
		return errors.New("os is required")
	}
	if o.Engine != "" && o.Kind != OpBuild && o.Kind != OpPull {
		return fmt.Errorf("--engine is not supported for %s", o.Kind)
	}

	listCommand := string(o.Kind)
	if o.Type == "" && ctx.Catalog.HasTypedTargets(listCommand, o.OS) {
		o.Type = defaultOSType
	}
	matches := ctx.Catalog.Match(listCommand, o.OS, o.Type, o.Arch, o.Engine)
	if len(matches) == 0 {
		return fmt.Errorf("target os=%s type=%s arch=%s engine=%s is not listed by `sailwright %s list` on this host", o.OS, o.Type, o.Arch, o.Engine, listCommand)
	}
	// Types only exist for OSes that list them; drop a type the CLI would ignore.
	if matches[0].Type == "" {
		o.Type = ""
	}

	if o.Kind == OpProvision {
		if o.Verbosity < -1 || o.Verbosity > maxVerbosity {
			return fmt.Errorf("verbosity must be between 0 and %d", maxVerbosity)
		}
		if o.Playbook != "" && (!playbookPattern.MatchString(o.Playbook) || !containsString(ctx.Playbooks, o.Playbook)) {
			return fmt.Errorf("playbook %q is not an embedded playbook (available: %s)", o.Playbook, strings.Join(ctx.Playbooks, ", "))
		}
	}
	return nil
}

func validateHelpPath(helpPath []string) error {
	switch len(helpPath) {
	case 0:
		return nil
	case 1:
		if containsString(helpCommands, helpPath[0]) {
			return nil
		}
	case 2:
		if helpPath[1] == "list" && containsString(listCommands, helpPath[0]) {
			return nil
		}
	}
	return fmt.Errorf("help for %q is not available", strings.Join(helpPath, " "))
}

var helpCommands = append([]string{"install"}, listCommands...)

func validatePullReference(ref string, allowlist []string) error {
	if len(ref) == 0 || len(ref) > maxReferenceSize || !ociReferencePattern.MatchString(ref) {
		return fmt.Errorf("invalid OCI reference %q", ref)
	}
	repository := ref
	if at := strings.Index(repository, "@"); at >= 0 {
		repository = repository[:at]
	}
	if slash := strings.LastIndex(repository, "/"); slash >= 0 {
		if colon := strings.LastIndex(repository[slash:], ":"); colon >= 0 {
			repository = repository[:slash+colon]
		}
	}
	for _, prefix := range allowlist {
		if repository == prefix || strings.HasPrefix(repository, prefix+"/") {
			return nil
		}
	}
	return fmt.Errorf("OCI reference %q is not covered by the gateway pull_allowlist", ref)
}

// flagSpec describes one flag of the safe grammar.
type flagSpec struct {
	name  string
	short string
	bool  bool
}

var safeFlags = map[OpKind][]flagSpec{
	OpBuild: {
		{name: "arch", short: "a"}, {name: "type", short: "t"}, {name: "engine"},
		{name: "parallel", short: "p"}, {name: "headless", bool: true},
		{name: "verbose", short: "v", bool: true}, {name: "no-cache", bool: true},
	},
	OpCreate:    {{name: "arch", short: "a"}, {name: "type", short: "t"}},
	OpStart:     {{name: "arch", short: "a"}, {name: "type", short: "t"}},
	OpStop:      {{name: "arch", short: "a"}, {name: "type", short: "t"}},
	OpDestroy:   {{name: "arch", short: "a"}, {name: "type", short: "t"}},
	OpProvision: {{name: "arch", short: "a"}, {name: "type", short: "t"}, {name: "check", bool: true}, {name: "verbosity"}, {name: "playbook"}},
	OpPull: {
		{name: "os"}, {name: "arch", short: "a"}, {name: "type", short: "t"}, {name: "engine"},
		{name: "yes", short: "y", bool: true}, {name: "no-docker-credentials", bool: true},
	},
}

// ParseSafe tries to map free-form sailwright arguments onto a safe operation. An
// error means "not in the safe set" and carries the reason shown to the approver.
func ParseSafe(args []string, ctx GrammarContext) (Operation, error) {
	if len(args) == 0 {
		return Operation{}, errors.New("no arguments")
	}
	if len(args) > maxArgs {
		return Operation{}, fmt.Errorf("more than %d arguments", maxArgs)
	}
	for _, arg := range args {
		if len(arg) > maxArgLength {
			return Operation{}, fmt.Errorf("argument longer than %d bytes", maxArgLength)
		}
		if arg == "--" {
			return Operation{}, errors.New("passthrough arguments after -- are not in the safe set")
		}
	}

	if last := args[len(args)-1]; last == "--help" || last == "-h" {
		op := Operation{Kind: OpHelp, HelpPath: append([]string{}, args[:len(args)-1]...)}
		return op, op.Validate(ctx)
	}

	command := args[0]
	if len(args) >= 2 && args[1] == "list" {
		rest := args[2:]
		if len(rest) > 1 || (len(rest) == 1 && rest[0] != "--json") {
			return Operation{}, fmt.Errorf("unsupported arguments for %s list", command)
		}
		op := Operation{Kind: OpList, Command: command}
		return op, op.Validate(ctx)
	}

	kind := OpKind(command)
	specs, ok := safeFlags[kind]
	if !ok {
		return Operation{}, fmt.Errorf("command %q is not in the safe set", command)
	}

	positionals, values, err := parseFlags(args[1:], specs)
	if err != nil {
		return Operation{}, err
	}
	if len(positionals) != 1 {
		return Operation{}, fmt.Errorf("%s expects exactly one positional argument", command)
	}

	op := Operation{Kind: kind, Verbosity: -1, Arch: values["arch"], Type: values["type"], Engine: values["engine"]}
	switch kind {
	case OpPull:
		op.Ref = positionals[0]
		op.OS = values["os"]
		if v, ok := values["yes"]; ok && v != "true" {
			return Operation{}, errors.New("pull without --yes is not in the safe set")
		}
		if v, ok := values["no-docker-credentials"]; ok && v != "true" {
			return Operation{}, errors.New("pull with Docker credentials is not in the safe set")
		}
	default:
		op.OS = positionals[0]
	}

	switch kind {
	case OpBuild:
		if parallel, ok := values["parallel"]; ok && parallel != "1" {
			return Operation{}, errors.New("parallel builds are not in the safe set")
		}
		op.Headless = values["headless"] == "true"
		op.Verbose = values["verbose"] == "true"
		op.NoCache = values["no-cache"] == "true"
	case OpProvision:
		op.Check = values["check"] == "true"
		if raw, ok := values["verbosity"]; ok {
			verbosity, err := strconv.Atoi(raw)
			if err != nil || strconv.Itoa(verbosity) != raw {
				return Operation{}, fmt.Errorf("invalid verbosity %q", raw)
			}
			op.Verbosity = verbosity
		}
		if playbook, ok := values["playbook"]; ok {
			name, ok := embeddedPlaybookName(playbook, ctx)
			if !ok {
				return Operation{}, errors.New("custom playbook paths run Ansible code on the host and need approval")
			}
			op.Playbook = name
		}
	}

	if err := op.Validate(ctx); err != nil {
		return Operation{}, err
	}
	return op, nil
}

// embeddedPlaybookName accepts only the exact gateway-owned path of an embedded playbook.
func embeddedPlaybookName(value string, ctx GrammarContext) (string, bool) {
	for _, name := range ctx.Playbooks {
		if ctx.PlaybookDir != "" && value == embeddedPlaybookPath(ctx.PlaybookDir, name) {
			return name, true
		}
	}
	return "", false
}

// parseFlags parses --name value, --name=value, -s value and boolean flags. Unknown
// flags, duplicates, combined short flags and empty values are errors.
func parseFlags(args []string, specs []flagSpec) ([]string, map[string]string, error) {
	values := map[string]string{}
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		var name, value string
		hasValue := false
		switch {
		case strings.HasPrefix(arg, "--"):
			name = strings.TrimPrefix(arg, "--")
			if eq := strings.Index(name, "="); eq >= 0 {
				name, value, hasValue = name[:eq], name[eq+1:], true
			}
		default:
			name = strings.TrimPrefix(arg, "-")
			if len(name) != 1 {
				return nil, nil, fmt.Errorf("unsupported short flag syntax %q", arg)
			}
		}

		spec, ok := lookupFlag(specs, name, !strings.HasPrefix(arg, "--"))
		if !ok {
			return nil, nil, fmt.Errorf("flag %q is not in the safe set", arg)
		}
		if _, dup := values[spec.name]; dup {
			return nil, nil, fmt.Errorf("flag --%s given more than once", spec.name)
		}

		if spec.bool {
			if !hasValue {
				value = "true"
			} else if value != "true" && value != "false" {
				return nil, nil, fmt.Errorf("invalid boolean value for --%s", spec.name)
			}
		} else if !hasValue {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag --%s needs a value", spec.name)
			}
			i++
			value = args[i]
		}
		if !spec.bool && (value == "" || strings.HasPrefix(value, "-")) {
			return nil, nil, fmt.Errorf("invalid value for --%s", spec.name)
		}
		values[spec.name] = value
	}
	return positionals, values, nil
}

func lookupFlag(specs []flagSpec, name string, short bool) (flagSpec, bool) {
	for _, spec := range specs {
		if (!short && spec.name == name) || (short && spec.short != "" && spec.short == name) {
			return spec, true
		}
	}
	return flagSpec{}, false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
