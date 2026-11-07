//go:build !unix && !windows

package build

import "fmt"

func availableDiskBytes(string) (uint64, error) { return 0, fmt.Errorf("disk telemetry unavailable") }
