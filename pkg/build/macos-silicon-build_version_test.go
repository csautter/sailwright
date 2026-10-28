package build

import (
	"strings"
	"testing"
)

func TestMacOSQemuVersionCheck(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		output  string
		wantOK  bool
		wantMsg string
	}{
		{name: "tested 11", output: "QEMU emulator version 11.1.2\nCopyright (c) 2003-2026 Fabrice Bellard", wantOK: true, wantMsg: "QEMU 11.1"},
		{name: "tested 10", output: "QEMU emulator version 10.0.3", wantOK: true, wantMsg: "QEMU 10.0"},
		{name: "untested major", output: "QEMU emulator version 12.0.0", wantOK: false, wantMsg: "has not been tested"},
		{name: "too old for virt-10.0", output: "QEMU emulator version 9.2.4", wantOK: false, wantMsg: "virt-10.0 machine needs QEMU 10.0"},
		{name: "unparsable", output: "something else", wantOK: false, wantMsg: "could not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			msg, ok := macOSQemuVersionCheck(tc.output)
			if ok != tc.wantOK || !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("macOSQemuVersionCheck(%q) = %q, %v; want %v and message containing %q", tc.output, msg, ok, tc.wantOK, tc.wantMsg)
			}
		})
	}
}
