package build

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func availableMemoryMB() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0, err
	}
	var pageSize, pages uint64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "page size of") {
			for i, field := range strings.Fields(line) {
				fields := strings.Fields(line)
				if field == "of" && i+1 < len(fields) {
					pageSize, _ = strconv.ParseUint(fields[i+1], 10, 64)
				}
			}
		}
		if strings.HasPrefix(line, "Pages free:") || strings.HasPrefix(line, "Pages inactive:") {
			fields := strings.Fields(line)
			n, _ := strconv.ParseUint(strings.TrimSuffix(fields[len(fields)-1], "."), 10, 64)
			pages += n
		}
	}
	if pageSize == 0 {
		return 0, fmt.Errorf("vm_stat did not report page size")
	}
	return pages * pageSize / (1 << 20), nil
}
