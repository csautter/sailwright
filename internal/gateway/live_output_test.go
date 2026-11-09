package gateway

import (
	"runtime"
	"strings"
	"testing"
)

func TestLiveOutputCommand(t *testing.T) {
	got := liveOutputCommand(`C:\Users\o'neil\log.log`)
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(got, "Get-Content -Wait") || !strings.Contains(got, "o''neil") {
			t.Fatalf("unexpected command: %s", got)
		}
		return
	}
	if !strings.HasPrefix(got, "tail -f ") {
		t.Fatalf("unexpected command: %s", got)
	}
}
