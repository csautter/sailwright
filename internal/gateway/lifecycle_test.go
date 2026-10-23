package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordLifecycleWithoutTCPListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	log, err := OpenAuditLog(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{audit: log, version: "test"}
	g.recordLifecycle("gateway_started", &Listeners{SocketPath: "/run/agent/gateway.sock"})
	_ = log.Close()
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"event":"gateway_started"`) || strings.Contains(string(data), `"tcp"`) || !strings.Contains(string(data), "gateway.sock") {
		t.Fatalf("unexpected lifecycle event: %s", data)
	}
}

func TestTrimHistoryRemovesJobLogs(t *testing.T) {
	logs := t.TempDir()
	m := &JobManager{logsDir: logs, jobs: map[string]*job{}}
	for i := 0; i <= maxFinishedJobs; i++ {
		id := fmt.Sprintf("job%03d", i)
		m.jobs[id] = &job{}
		m.order = append(m.order, id)
		if err := os.WriteFile(filepath.Join(logs, id+".log"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.trimHistoryLocked()
	if _, err := os.Stat(filepath.Join(logs, "job000.log")); !os.IsNotExist(err) {
		t.Fatalf("expected the oldest job log to be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(logs, "job001.log")); err != nil {
		t.Fatalf("newer logs must stay: %v", err)
	}
}
