package build

import (
	"os"
	"strings"
	"testing"
)

func TestQemuCommunicatorForwardingIsLoopbackOnly(t *testing.T) {
	for path, port := range map[string]string{
		"../../build/packer/windows/windows11-qemu.pkr.hcl":         "5985",
		"../../build/packer/linux/ubuntu/linux-ubuntu-qemu.pkr.hcl": "22",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Both architecture-specific argument lists must override the plugin's wildcard binding.
		want := `"-netdev", "user,id=user.0,hostfwd=tcp:127.0.0.1:{{ .SSHHostPort }}-:` + port + `"`
		_, args, ok := strings.Cut(string(content), "qemu_args = {")
		if !ok || strings.Count(args, want) != 2 {
			t.Errorf("%s must restrict both communicator forwards to loopback", path)
		}
	}
}
