package gateway

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testGrammar() GrammarContext {
	catalog := NewTargetCatalog()
	vmTargets := []Target{
		{OS: "ubuntu", Type: "server", Arch: "amd64", Engine: "qemu"},
		{OS: "ubuntu", Type: "desktop", Arch: "amd64", Engine: "qemu"},
		{OS: "ubuntu", Type: "server", Arch: "arm64", Engine: "qemu"},
		{OS: "windows11", Arch: "amd64", Engine: "hyperv"},
		{OS: "windows11", Arch: "amd64", Engine: "virtualbox"},
	}
	for _, command := range listCommands {
		catalog.Set(command, vmTargets, nil)
	}
	catalog.Set("provision", append(vmTargets, Target{OS: "local", Engine: "local"}), nil)
	playbookDir := filepath.Join(string(filepath.Separator), "state", "data", "app", "project", "playbooks")
	return GrammarContext{
		Catalog:       catalog,
		PlaybookDir:   playbookDir,
		Playbooks:     []string{"role-sources-test", "setup"},
		PullAllowlist: []string{"ghcr.io/csautter/sailwright"},
	}
}

func TestParseSafeAcceptsSafeCommandsAndRendersCanonicalArgv(t *testing.T) {
	ctx := testGrammar()
	setupPath := embeddedPlaybookPath(ctx.PlaybookDir, "setup")
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"create", "ubuntu"}, []string{"create", "ubuntu", "--arch", "amd64", "--type", "server"}},
		{[]string{"start", "ubuntu", "-t", "desktop", "-a", "amd64"}, []string{"start", "ubuntu", "--arch", "amd64", "--type", "desktop"}},
		{[]string{"stop", "windows11", "--arch=amd64"}, []string{"stop", "windows11", "--arch", "amd64"}},
		{[]string{"destroy", "windows11", "--type", "server"}, []string{"destroy", "windows11", "--arch", "amd64"}},
		{[]string{"build", "windows11", "--engine", "hyperv", "--headless", "--no-cache", "-v"}, []string{"build", "windows11", "--arch", "amd64", "--engine", "hyperv", "--headless", "--verbose", "--no-cache"}},
		{[]string{"build", "ubuntu", "--parallel", "1"}, []string{"build", "ubuntu", "--arch", "amd64", "--type", "server"}},
		{[]string{"provision", "ubuntu", "--check", "--verbosity", "2"}, []string{"provision", "ubuntu", "--arch", "amd64", "--type", "server", "--check", "--verbosity", "2"}},
		{[]string{"provision", "ubuntu", "--playbook", setupPath}, []string{"provision", "ubuntu", "--arch", "amd64", "--type", "server", "--playbook", setupPath}},
		{[]string{"provision", "ubuntu", "--check=false"}, []string{"provision", "ubuntu", "--arch", "amd64", "--type", "server"}},
		{[]string{"pull", "ghcr.io/csautter/sailwright/ubuntu-server-amd64:qemu", "--os", "ubuntu"}, []string{"pull", "ghcr.io/csautter/sailwright/ubuntu-server-amd64:qemu", "--os", "ubuntu", "--arch", "amd64", "--type", "server", "--yes", "--no-docker-credentials"}},
		{[]string{"pull", "ghcr.io/csautter/sailwright/win:hyperv", "--os", "windows11", "--engine", "hyperv", "-y", "--no-docker-credentials"}, []string{"pull", "ghcr.io/csautter/sailwright/win:hyperv", "--os", "windows11", "--arch", "amd64", "--engine", "hyperv", "--yes", "--no-docker-credentials"}},
		{[]string{"create", "list"}, []string{"create", "list", "--json"}},
		{[]string{"provision", "list", "--json"}, []string{"provision", "list", "--json"}},
		{[]string{"--help"}, []string{"--help"}},
		{[]string{"provision", "-h"}, []string{"provision", "--help"}},
		{[]string{"build", "list", "--help"}, []string{"build", "list", "--help"}},
	}
	for _, tc := range cases {
		op, err := ParseSafe(tc.args, ctx)
		if err != nil {
			t.Fatalf("ParseSafe(%q) unexpectedly not safe: %v", tc.args, err)
		}
		if got := op.Argv(ctx); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("ParseSafe(%q) argv = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestParseSafeRejectsEverythingOutsideTheSafeSet(t *testing.T) {
	ctx := testGrammar()
	long := strings.Repeat("a", 40)
	cases := [][]string{
		{},
		{"provision", "ubuntu", "--playbook=/x"},
		{"provision", "ubuntu", "--playbook", "/workspaces/repo/site.yml"},
		{"provision", "ubuntu", "--playbook", "setup"},
		{"provision", "ubuntu", "--playbook", filepath.Join(ctx.PlaybookDir, "..", "evil.yml")},
		{"provision", "ubuntu", "--", "-e", "@f"},
		{"provision", "ubuntu", "--inventory-path", "/tmp/inv"},
		{"provision", "local", "--check"},
		{"provision", "local"},
		{"provision", "ubuntu", "--dry-run"},
		{"provision", "ubuntu", "--verbosity", "9"},
		{"provision", "ubuntu", "--verbosity", "02"},
		{"create", "all"},
		{"destroy", "all"},
		{"build", "all"},
		{"build", "ubuntu", "--parallel", "4"},
		{"push", "ghcr.io/csautter/sailwright/x:y", "--os", "ubuntu"},
		{"pull", "ghcr.io/evil/x:y", "--os", "ubuntu"},
		{"pull", "ghcr.io/csautter/sailwright-evil/x:y", "--os", "ubuntu"},
		{"pull", "ghcr.io/csautter/sailwright/x:y", "--os", "ubuntu", "--password", "secret"},
		{"pull", "ghcr.io/csautter/sailwright/x:y", "--os", "ubuntu", "--plain-http"},
		{"pull", "ghcr.io/csautter/sailwright/x:y", "--os", "ubuntu", "--yes=false"},
		{"pull", "ghcr.io/csautter/sailwright/x:y", "--os", "ubuntu", "--host-os", "windows"},
		{"install"},
		{"extension", "run", "foo"},
		{"completion", "bash"},
		{"create", "ubuntu", "--unknown"},
		{"create", "ubuntu", "--arch", "amd64", "--arch", "arm64"},
		{"create", "ubuntu", "-at", "amd64"},
		{"create", "ubuntu;", "rm", "-rf", "/"},
		{"create", "ubuntu;rm"},
		{"create", "$(id)"},
		{"create", "`id`"},
		{"create", "../ubuntu"},
		{"create", "ubuntu", "--type", "../server"},
		{"create", "ubuntu", "--arch", "amd64 "},
		{"create", "ubuntu", "--arch", ""},
		{"create", "ubuntu", "--arch"},
		{"create", "ubuntu", "--arch", "--type"},
		{"create", "ubuntu", "extra"},
		{"create"},
		{"create", "Ubuntu"},
		{"create", "ubuntu\u0430"}, // Cyrillic "a"
		{"create", "ubuntu", "--type", "server\u200b"},
		{"create", long},
		{"create", "macos"},
		{"create", "ubuntu", "--arch", "riscv64"},
		{"create", "ubuntu", "--engine", "qemu"},
		{"create", "list", "--all"},
		{"create", "list", "--json", "--json"},
		{"provision", "local", "--help"},
		{"build", "--arch", "x", "--help"},
		{"help", "build"},
	}
	for _, args := range cases {
		if op, err := ParseSafe(args, ctx); err == nil {
			t.Fatalf("ParseSafe(%q) classified as safe: %+v -> %q", args, op, op.Argv(ctx))
		}
	}
}

func TestParseSafeRejectsTooManyOrTooLongArguments(t *testing.T) {
	ctx := testGrammar()
	many := make([]string, maxArgs+1)
	for i := range many {
		many[i] = "x"
	}
	if _, err := ParseSafe(many, ctx); err == nil {
		t.Fatal("expected too many arguments to be rejected")
	}
	if _, err := ParseSafe([]string{"create", strings.Repeat("a", maxArgLength+1)}, ctx); err == nil {
		t.Fatal("expected an overlong argument to be rejected")
	}
}

func TestOperationValidateRejectsUnlistedTypedToolTargets(t *testing.T) {
	ctx := testGrammar()
	cases := []Operation{
		{Kind: OpCreate, OS: "ubuntu", Arch: "arm64", Type: "desktop", Verbosity: -1},
		{Kind: OpProvision, OS: "local", Verbosity: -1},
		{Kind: OpProvision, OS: "ubuntu", Verbosity: 5},
		{Kind: OpProvision, OS: "ubuntu", Verbosity: -1, Playbook: "../setup"},
		{Kind: OpProvision, OS: "ubuntu", Verbosity: -1, Playbook: "missing"},
		{Kind: OpPull, OS: "ubuntu", Ref: "docker.io/library/ubuntu:latest", Verbosity: -1},
		{Kind: OpKind("install"), Verbosity: -1},
		{Kind: OpList, Command: "install"},
		{Kind: OpHelp, HelpPath: []string{"extension"}},
	}
	for _, op := range cases {
		if err := op.Validate(ctx); err == nil {
			t.Fatalf("expected %+v to be rejected", op)
		}
	}
}

func TestCanonicalArgvRoundTrips(t *testing.T) {
	ctx := testGrammar()
	ops := []Operation{
		{Kind: OpBuild, OS: "ubuntu", Type: "desktop", Arch: "amd64", Headless: true, Verbose: true, NoCache: true, Verbosity: -1},
		{Kind: OpBuild, OS: "windows11", Arch: "amd64", Engine: "virtualbox", Verbosity: -1},
		{Kind: OpCreate, OS: "ubuntu", Verbosity: -1},
		{Kind: OpStart, OS: "windows11", Verbosity: -1},
		{Kind: OpStop, OS: "ubuntu", Arch: "arm64", Verbosity: -1},
		{Kind: OpDestroy, OS: "ubuntu", Type: "desktop", Verbosity: -1},
		{Kind: OpProvision, OS: "ubuntu", Check: true, Verbosity: 0, Playbook: "setup"},
		{Kind: OpProvision, OS: "windows11", Verbosity: -1},
		{Kind: OpPull, OS: "ubuntu", Ref: "ghcr.io/csautter/sailwright/ubuntu:qemu", Verbosity: -1},
	}
	for _, op := range ops {
		if err := op.Validate(ctx); err != nil {
			t.Fatalf("validate %+v: %v", op, err)
		}
		argv := op.Argv(ctx)
		parsed, err := ParseSafe(argv, ctx)
		if err != nil {
			t.Fatalf("canonical argv %q is not safe: %v", argv, err)
		}
		if !reflect.DeepEqual(parsed, op) {
			t.Fatalf("round trip mismatch:\n got  %+v\n want %+v", parsed, op)
		}
	}
}

func TestValidatePullReference(t *testing.T) {
	allow := []string{"ghcr.io/csautter/sailwright", "localhost:5000/sailwright"}
	good := []string{
		"ghcr.io/csautter/sailwright/ubuntu-server-amd64:qemu",
		"ghcr.io/csautter/sailwright/a/b@sha256:" + strings.Repeat("a", 64),
		"localhost:5000/sailwright/x",
		// An allowlist entry may also name a complete repository.
		"ghcr.io/csautter/sailwright:latest",
	}
	bad := []string{
		"ghcr.io/csautter/sailwrightx/a:b",
		"ghcr.io/csautter/sailwright/../x:y",
		"GHCR.io/csautter/sailwright/a:b",
		"https://ghcr.io/csautter/sailwright/a:b",
		"ghcr.io/csautter/sailwright/a:b c",
		"localhost:5000/other/x",
		"",
	}
	for _, ref := range good {
		if err := validatePullReference(ref, allow); err != nil {
			t.Fatalf("expected %q to be allowed: %v", ref, err)
		}
	}
	for _, ref := range bad {
		if err := validatePullReference(ref, allow); err == nil {
			t.Fatalf("expected %q to be rejected", ref)
		}
	}
	if err := validatePullReference(good[0], nil); err == nil {
		t.Fatal("an empty allowlist must reject every reference")
	}
}

// FuzzParseSafe checks two invariants for arbitrary input: a safe classification
// renders an argv that parses back to the same operation, and that argv contains no
// passthrough marker, no "local"/"all" target and only values from the grammar.
func FuzzParseSafe(f *testing.F) {
	seeds := [][]string{
		{"create", "ubuntu"},
		{"provision", "ubuntu", "--check", "--verbosity", "3"},
		{"provision", "local"},
		{"build", "windows11", "--engine", "hyperv"},
		{"pull", "ghcr.io/csautter/sailwright/x:y", "--os", "ubuntu"},
		{"provision", "ubuntu", "--", "-e", "x=1"},
	}
	for _, seed := range seeds {
		f.Add(strings.Join(seed, "\x00"))
	}
	ctx := testGrammar()
	f.Fuzz(func(t *testing.T, joined string) {
		args := strings.Split(joined, "\x00")
		op, err := ParseSafe(args, ctx)
		if err != nil {
			return
		}
		argv := op.Argv(ctx)
		for _, arg := range argv {
			if arg == "--" || arg == "local" || arg == "all" {
				t.Fatalf("safe argv %q contains %q", argv, arg)
			}
		}
		again, err := ParseSafe(argv, ctx)
		if err != nil {
			t.Fatalf("canonical argv %q of safe input %q is not safe: %v", argv, args, err)
		}
		if !reflect.DeepEqual(again, op) {
			t.Fatalf("round trip mismatch for %q: %+v vs %+v", args, again, op)
		}
	})
}
