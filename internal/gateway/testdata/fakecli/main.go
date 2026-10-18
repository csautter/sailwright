// Command fakecli imitates the sailwright CLI for gateway and E2E tests. It records
// every invocation (argv, cwd, environment) as a JSON line in $FAKE_CLI_RECORD and
// keeps VM state in $FAKE_CLI_STATE so list output reflects create/start/stop/destroy.
//
// Special targets: os "failos" exits 3, os "slowos" sleeps for a minute.
//
// build writes console frames like the real VNC recording into
// $DEV_ALCHEMY_CACHE_DIR/<os>/qemu-out-<slug>-vncsnapshot.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type invocation struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
	Env  []string `json:"env"`
}

type target struct {
	OS     string            `json:"os"`
	Type   string            `json:"type"`
	Arch   string            `json:"arch"`
	Engine string            `json:"engine"`
	Fields map[string]string `json:"fields"`
	Addr   *string           `json:"address"`
}

var baseTargets = []target{
	{OS: "ubuntu", Type: "server", Arch: "amd64", Engine: "qemu"},
	{OS: "ubuntu", Type: "desktop", Arch: "amd64", Engine: "qemu"},
	{OS: "windows11", Arch: "amd64", Engine: "qemu"},
	{OS: "failos", Arch: "amd64", Engine: "qemu"},
	{OS: "slowos", Arch: "amd64", Engine: "qemu"},
	// Listed as unstable, like an emulated architecture.
	{OS: "ubuntu", Type: "server", Arch: "arm64", Engine: "qemu"},
}

func main() {
	record()
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Println("Usage: sailwright [command]")
		return
	}
	if args[0] == "__session" {
		// Reports how the gateway started this process.
		fmt.Println(sessionInfo())
		return
	}
	if args[len(args)-1] == "--help" {
		fmt.Printf("Usage: sailwright %s [flags]\n", strings.Join(args[:len(args)-1], " "))
		return
	}
	if len(args) >= 2 && args[1] == "list" {
		printListing(args[0])
		return
	}

	command := args[0]
	osName := ""
	if len(args) > 1 {
		osName = args[1]
	}
	switch osName {
	case "failos":
		fmt.Println("simulated failure")
		os.Exit(3)
	case "slowos":
		fmt.Println("sleeping")
		time.Sleep(time.Minute)
		return
	}

	if command != "install" && command != "pull" && !knownOS(osName) {
		fmt.Fprintf(os.Stderr, "Error: ❌ invalid combination: OS=%s\n", osName)
		os.Exit(1)
	}

	id := targetID(args)
	switch command {
	case "build":
		writeConsoleFrames(args)
		fmt.Println("build ok")
	case "install", "pull":
		fmt.Printf("%s ok\n", command)
	case "create":
		writeState(id, "stopped")
		fmt.Println("created")
	case "start":
		writeState(id, "running")
		fmt.Println("started")
	case "stop":
		if readState(id) != "" {
			writeState(id, "stopped")
		}
		fmt.Println("stopped")
	case "destroy":
		removeState(id)
		fmt.Println("destroyed")
	case "provision":
		printPlaybook(args)
		changed := 2
		if readState(id+".provisioned") != "" && !contains(args, "--check") {
			changed = 0
		}
		if !contains(args, "--check") {
			writeState(id+".provisioned", "yes")
		}
		fmt.Printf("PLAY RECAP *********\n%s : ok=5 changed=%d unreachable=0 failed=0 skipped=1\n", osName, changed)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(1)
	}
}

// printPlaybook echoes the playbook content so tests can see what was executed.
func printPlaybook(args []string) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--become-password-file" {
			// Echo the password so tests can check that it is redacted.
			if data, err := os.ReadFile(args[i+1]); err == nil { // #nosec G304 -- test fixture.
				fmt.Printf("BECOME-PASSWORD: %s\n", data)
			}
		}
		if args[i] == "--playbook" {
			if data, err := os.ReadFile(args[i+1]); err == nil {
				fmt.Printf("PLAYBOOK: %s\n", strings.TrimSpace(string(data)))
			}
		}
	}
}

func printListing(command string) {
	targets := append([]target(nil), baseTargets...)
	if command == "provision" {
		targets = append(targets, target{OS: "local", Engine: "local"})
	}
	for i := range targets {
		state := readState(fmt.Sprintf("%s-%s-%s", targets[i].OS, targets[i].Type, targets[i].Arch))
		if state == "" {
			state = "missing"
		}
		start := "ready to start"
		switch state {
		case "missing":
			start = "create required"
		case "running":
			start = "already running"
		}
		status := "stable"
		if targets[i].Arch == "arm64" {
			status = "unstable"
		}
		// Mirrors the columns of the real `start list` and `create list`.
		targets[i].Fields = map[string]string{"state": state, "start": start, "status": status}
	}
	out, _ := json.Marshal(map[string]any{"schema_version": 1, "targets": targets})
	fmt.Println("diagnostic line before the document")
	fmt.Println(string(out))
}

func targetID(args []string) string {
	osName, osType, arch := "", "", "amd64"
	if len(args) > 1 {
		osName = args[1]
	}
	for i := 2; i < len(args)-1; i++ {
		switch args[i] {
		case "--type":
			osType = args[i+1]
		case "--arch":
			arch = args[i+1]
		}
	}
	return fmt.Sprintf("%s-%s-%s", osName, osType, arch)
}

// writeConsoleFrames writes two frames: a dark screen and one with a bright "dialog".
func writeConsoleFrames(args []string) {
	cache := os.Getenv("DEV_ALCHEMY_CACHE_DIR")
	if cache == "" {
		return
	}
	osName, osType, arch := args[1], "", "amd64"
	for i := 2; i < len(args)-1; i++ {
		switch args[i] {
		case "--type":
			osType = args[i+1]
		case "--arch":
			arch = args[i+1]
		}
	}
	slug := osName
	if osType != "" {
		slug += "-" + osType
	}
	dir := filepath.Join(cache, osName, "qemu-out-"+slug+"-"+arch+"-vncsnapshot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	for i, dialog := range []bool{false, true} {
		img := image.NewGray(image.Rect(0, 0, 320, 240))
		for y := 0; y < 240; y++ {
			for x := 0; x < 320; x++ {
				value := uint8(20)
				if dialog && x > 80 && x < 240 && y > 60 && y < 180 {
					value = 230
				}
				img.SetGray(x, y, color.Gray{Y: value})
			}
		}
		path := filepath.Join(dir, fmt.Sprintf("qemu.vnc%05d.jpg", i+1))
		f, err := os.Create(path) // #nosec G304 -- test fixture path below the managed cache dir.
		if err != nil {
			return
		}
		_ = jpeg.Encode(f, img, nil)
		_ = f.Close()
		// Distinct modification times order the frames.
		later := time.Now().Add(time.Duration(i) * time.Second)
		_ = os.Chtimes(path, later, later)
	}
}

func record() {
	path := os.Getenv("FAKE_CLI_RECORD")
	if path == "" {
		return
	}
	cwd, _ := os.Getwd()
	env := os.Environ()
	sort.Strings(env)
	line, _ := json.Marshal(invocation{Argv: os.Args[1:], Cwd: cwd, Env: env})
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- test fixture path from the harness.
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func stateFile(id string) string {
	dir := os.Getenv("FAKE_CLI_STATE")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id)
}

func readState(id string) string {
	path := stateFile(id)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path from the harness.
	if err != nil {
		return ""
	}
	return string(data)
}

func writeState(id, state string) {
	if path := stateFile(id); path != "" {
		_ = os.WriteFile(path, []byte(state), 0o600)
	}
}

func removeState(id string) {
	if path := stateFile(id); path != "" {
		_ = os.Remove(path)
		_ = os.Remove(path + ".provisioned")
	}
}

func knownOS(osName string) bool {
	if osName == "local" {
		return true
	}
	for _, t := range baseTargets {
		if t.OS == osName {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
