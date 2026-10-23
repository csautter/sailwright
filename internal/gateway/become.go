package gateway

import (
	"os"
	"runtime"
)

// WithBecomePasswordFile passes an Ansible become password file to `sailwright
// provision` as a passthrough argument.
func WithBecomePasswordFile(argv []string, path string) []string {
	out := append([]string(nil), argv...)
	if !containsString(out, "--") {
		out = append(out, "--")
	}
	return append(out, "--become-password-file", path)
}

// writeBecomePasswordFile stores the password in an owner-only file inside the
// gateway staging dir; the returned cleanup removes it.
func writeBecomePasswordFile(dir string, password string) (string, func(), error) {
	if err := ensurePrivateDir(dir); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(dir, "become-*")
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(privateFilePermission); err != nil && runtime.GOOS != "windows" {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if _, err := file.WriteString(password); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
