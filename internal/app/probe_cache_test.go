package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildReusesStoredProbeUntilExecutableChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX executable")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls.log")
	exe := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\nif [ \"$1\" = \"--version\" ]; then echo 1.0.0-test; fi\nexit 0\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(exe, past, past); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Backend = "larkcli"
	cfg.LarkCLI.Executable = exe
	cfg.Storage = StorageConfig{Type: "sqlite", Path: filepath.Join(dir, "sfs.db"), TTL: "45m"}
	build := func() *App {
		t.Helper()
		a, err := Build(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	countCalls := func() int {
		raw, err := os.ReadFile(calls)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(raw), "\n")
	}

	build()
	first := countCalls()
	if first == 0 {
		t.Fatal("first start should probe lark-cli")
	}
	a := build()
	if got := countCalls(); got != first {
		t.Fatalf("second start ran %d probe commands, want 0", got-first)
	}
	if !capabilityProbeNeeded(a.Engine, exe, time.Now().Add(capabilityCacheTTL+time.Minute)) {
		t.Fatal("stored probe results older than the TTL must not be reused")
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(exe, future, future); err != nil {
		t.Fatal(err)
	}
	build()
	if got := countCalls(); got == first {
		t.Fatal("start after lark-cli changed should probe again")
	}
}
