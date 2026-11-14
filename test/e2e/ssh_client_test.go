//go:build e2e

package e2e

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveWindowsSSHClient(t *testing.T) {
	system := filepath.Join("sys", "OpenSSH")
	git := filepath.Join("git", "usr", "bin")
	files := func(paths ...string) func(string) bool {
		return func(path string) bool {
			for _, candidate := range paths {
				if candidate == path {
					return true
				}
			}
			return false
		}
	}
	path := func(found map[string]string) func(string) (string, error) {
		return func(name string) (string, error) {
			if value, ok := found[name]; ok {
				return value, nil
			}
			return "", errors.New("not found")
		}
	}

	t.Run("complete system client", func(t *testing.T) {
		ssh, scp, err := resolveWindowsSSHClient(system,
			path(map[string]string{"ssh": filepath.Join(git, "ssh.exe")}),
			files(filepath.Join(system, "ssh.exe"), filepath.Join(system, "scp.exe")))
		if err != nil || ssh != filepath.Join(system, "ssh.exe") || scp != filepath.Join(system, "scp.exe") {
			t.Fatalf("got %q %q %v", ssh, scp, err)
		}
	})
	t.Run("server capability only: scp without ssh in System32", func(t *testing.T) {
		ssh, scp, err := resolveWindowsSSHClient(system,
			path(map[string]string{"ssh": filepath.Join(git, "ssh.exe"), "scp": filepath.Join(system, "scp.exe")}),
			files(filepath.Join(system, "scp.exe"), filepath.Join(git, "scp.exe")))
		if err != nil || ssh != filepath.Join(git, "ssh.exe") || scp != filepath.Join(git, "scp.exe") {
			t.Fatalf("scp must come from the directory of ssh, got %q %q %v", ssh, scp, err)
		}
	})
	t.Run("no sibling scp falls back to the path", func(t *testing.T) {
		other := filepath.Join("other", "scp.exe")
		ssh, scp, err := resolveWindowsSSHClient(system,
			path(map[string]string{"ssh": filepath.Join(git, "ssh.exe"), "scp": other}), files())
		if err != nil || ssh != filepath.Join(git, "ssh.exe") || scp != other {
			t.Fatalf("got %q %q %v", ssh, scp, err)
		}
	})
	t.Run("missing ssh", func(t *testing.T) {
		if _, _, err := resolveWindowsSSHClient(system, path(nil), files()); err == nil {
			t.Fatal("expected an error")
		}
	})
}
