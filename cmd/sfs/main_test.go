package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestIsLoopbackListen(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{address: "127.0.0.1:3765", want: true},
		{address: "localhost:3765", want: true},
		{address: "[::1]:3765", want: true},
		{address: ":3765", want: false},
		{address: "0.0.0.0:3765", want: false},
		{address: "192.168.1.10:3765", want: false},
		{address: "invalid", want: false},
	}
	for _, test := range tests {
		if got := isLoopbackListen(test.address); got != test.want {
			t.Errorf("isLoopbackListen(%q)=%v, want %v", test.address, got, test.want)
		}
	}
}

func TestCLIAskDisabledReturnsUnsupported(t *testing.T) {
	t.Setenv("SFS_AI_ENABLED", "false")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--backend", "mock", "--session-dir", t.TempDir(), "ask", "A 项目为什么延期"}, &stdout, &stderr)
	if err == nil || kernel.DetailFromError(err).Type != kernel.ErrUnsupported {
		t.Fatalf("expected unsupported ask error, got %v; stderr=%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("ask wrote output before failing: %s", stdout.String())
	}
}

func TestCLIMigrateSessionsIsIdempotent(t *testing.T) {
	sourceDir := t.TempDir()
	database := filepath.Join(t.TempDir(), "sessions.db")
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := kernel.SessionSnapshot{
		ID:        "rs_0123456789abcdefabcd",
		ScopeKey:  "test-scope",
		CreatedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour),
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, snapshot.ID+".json")
	if err := os.WriteFile(sourcePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	first := runMigration(t, sourceDir, database)
	if first.Scanned != 1 || first.Imported != 1 || first.Skipped != 0 {
		t.Fatalf("first migration report=%+v", first)
	}
	second := runMigration(t, sourceDir, database)
	if second.Scanned != 1 || second.Imported != 0 || second.AlreadyPresent != 1 || second.Skipped != 1 {
		t.Fatalf("second migration report=%+v", second)
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, raw) {
		t.Fatal("migration modified the source session file")
	}
}

func runMigration(t *testing.T, sourceDir, database string) session.JSONImportReport {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--output", "json", "migrate", "sessions", "--from", sourceDir, "--to", database}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("migrate sessions: %v; stderr=%s", err, stderr.String())
	}
	var report session.JSONImportReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode migration report: %v; stdout=%s", err, stdout.String())
	}
	return report
}
