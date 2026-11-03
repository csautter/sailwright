//go:build !linux && !darwin && !windows

package build

import "fmt"

func availableMemoryMB() (uint64, error) { return 0, fmt.Errorf("memory telemetry unavailable") }
