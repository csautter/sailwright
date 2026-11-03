package build

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func availableMemoryMB() (uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			return kb / 1024, err
		}
	}
	return 0, fmt.Errorf("MemAvailable is unavailable")
}
