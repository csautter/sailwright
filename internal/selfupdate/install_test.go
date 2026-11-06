package selfupdate

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallation(t *testing.T) {
	for _, tc := range []struct{ path, version, want string }{{"/opt/homebrew/Cellar/sailwright/1.0/bin/sailwright", "v1.0.0", "homebrew"}, {"/home/linuxbrew/.linuxbrew/Cellar/sailwright/1/bin/sailwright", "dev", "homebrew"}, {"/usr/bin/sailwright", "dev", "development"}, {"/usr/bin/sailwright", "v1.0.0", "standalone"}, {"/users/me/scoop/apps/sailwright/current/sailwright.exe", "v1.0.0", "package-manager"}} {
		if got := Installation(tc.path, tc.version); got != tc.want {
			t.Errorf("%s: %s", tc.path, got)
		}
	}
	if runtime.GOOS != "windows" {
		dir := t.TempDir()
		target := filepath.Join(dir, "Cellar", "sailwright")
		_ = os.MkdirAll(filepath.Dir(target), 0700)
		_ = os.WriteFile(target, []byte("old"), 0700)
		link := filepath.Join(dir, "sailwright")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if Installation(link, "v1.0.0") != "homebrew" {
			t.Fatal("symlink not detected")
		}
	}
}
func TestWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "sailwright")
	_ = os.WriteFile(binary, []byte("old"), 0755)
	if err := Writable(binary); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0555)
	defer os.Chmod(dir, 0755)
	if err := Writable(binary); err == nil {
		t.Fatal("unwritable directory accepted")
	}
}
func TestReplaceAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "preparation failure"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			cli, gw := filepath.Join(dir, "sailwright"), filepath.Join(dir, "sailwright-gateway")
			for _, p := range []string{cli, gw} {
				if err := os.WriteFile(p, []byte("old"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if fail {
				_ = os.WriteFile(gw+".old", []byte("keep backup"), 0600)
			}
			err := Replace([]Replacement{{cli, []byte("new cli")}, {gw, []byte("new gateway")}})
			if (err != nil) != fail {
				t.Fatalf("%v", err)
			}
			want := "new cli"
			if fail {
				want = "old"
			}
			b, _ := os.ReadFile(cli)
			if string(b) != want {
				t.Fatalf("cli: %s", b)
			}
			if fail {
				if runtime.GOOS != "windows" && !strings.Contains(err.Error(), "delete this backup file") {
					t.Fatalf("unclear backup recovery: %v", err)
				}
				b, _ := os.ReadFile(gw + ".old")
				if string(b) != "keep backup" {
					t.Fatal("existing backup lost")
				}
				b, _ = os.ReadFile(gw)
				if string(b) != "old" {
					t.Fatal("gateway changed")
				}
			}
			if runtime.GOOS != "windows" {
				info, _ := os.Stat(cli)
				if info.Mode().Perm() != 0755 {
					t.Fatal("permissions lost")
				}
			}
		})
	}
}

func TestReplaceRestoresBothAfterCommitFailure(t *testing.T) {
	dir := t.TempDir()
	cli, gateway := filepath.Join(dir, "sailwright"), filepath.Join(dir, "sailwright-gateway")
	for _, p := range []string{cli, gateway} {
		if err := os.WriteFile(p, []byte("original"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	fail := true
	rename := func(src, dst string) error {
		if fail && dst == gateway && strings.Contains(filepath.Base(src), ".sailwright-new-") {
			fail = false
			return fmt.Errorf("simulated rename failure")
		}
		return os.Rename(src, dst)
	}
	if err := replace([]Replacement{{cli, []byte("new cli")}, {gateway, []byte("new gateway")}}, rename, os.Link); err == nil {
		t.Fatal("expected commit failure")
	}
	for _, p := range []string{cli, gateway} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != "original" {
			t.Fatalf("%s: %s %v", p, b, err)
		}
	}
}

func TestReplaceWithoutHardLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows backs up by rename")
	}
	noLink := func(string, string) error { return fmt.Errorf("operation not permitted") }
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			cli, gateway := filepath.Join(dir, "sailwright"), filepath.Join(dir, "sailwright-gateway")
			for _, p := range []string{cli, gateway} {
				if err := os.WriteFile(p, []byte("original"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			rename := os.Rename
			if fail {
				rename = func(src, dst string) error {
					if dst == gateway && strings.Contains(filepath.Base(src), ".sailwright-new-") {
						return fmt.Errorf("simulated rename failure")
					}
					return os.Rename(src, dst)
				}
			}
			err := replace([]Replacement{{cli, []byte("new cli")}, {gateway, []byte("new gateway")}}, rename, noLink)
			if (err != nil) != fail {
				t.Fatalf("%v", err)
			}
			want := map[string]string{cli: "new cli", gateway: "new gateway"}
			if fail {
				want = map[string]string{cli: "original", gateway: "original"}
			}
			for p, w := range want {
				b, err := os.ReadFile(p)
				if err != nil || string(b) != w {
					t.Fatalf("%s: %s %v", p, b, err)
				}
				info, _ := os.Stat(p)
				if info.Mode().Perm() != 0755 {
					t.Fatalf("%s: permissions %v", p, info.Mode().Perm())
				}
				if _, err := os.Lstat(p + ".old"); !os.IsNotExist(err) {
					t.Fatalf("%s: backup left behind", p)
				}
			}
		})
	}
}

func TestUpdateLock(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "sailwright")
	// An old exclusive-create lock must not block a later update.
	if err := os.WriteFile(executable+".update-lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := updateLock(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if release, err := updateLock(executable); err == nil {
		release()
		t.Fatal("concurrent updater acquired lock")
	}
	unlock()
	release, err := updateLock(executable)
	if err != nil {
		t.Fatalf("lock not recovered: %v", err)
	}
	release()
}

func TestUpdateLockReleasedAfterCrash(t *testing.T) {
	if executable := os.Getenv("SAILWRIGHT_TEST_LOCK_TARGET"); executable != "" {
		unlock, err := updateLock(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		fmt.Println("locked")
		// Block until the parent kills this process, without deferred cleanup.
		_, _ = os.Stdin.Read(make([]byte, 1))
		return
	}
	executable := filepath.Join(t.TempDir(), "sailwright")
	child := exec.Command(os.Args[0], "-test.run=^TestUpdateLockReleasedAfterCrash$")
	child.Env = append(os.Environ(), "SAILWRIGHT_TEST_LOCK_TARGET="+executable)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child: %q %v", line, err)
	}
	if unlock, err := updateLock(executable); err == nil {
		unlock()
		t.Fatal("live child lock ignored")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	unlock, err := updateLock(executable)
	if err != nil {
		t.Fatalf("killed updater still blocks updates: %v", err)
	}
	unlock()
}
