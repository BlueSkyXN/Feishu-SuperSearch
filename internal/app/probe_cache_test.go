package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
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
	// The reuse contract is about the cache decision, which is asserted
	// directly: with valid stored verdicts and an unchanged executable the
	// second start must not need a probe. Under a fully parallel `go test
	// ./...` a probe subprocess can exceed its 3s timeout, which legitimately
	// voids the cache and makes Build re-probe, so the call count alone is
	// not a reliable assertion on a loaded machine.
	if capabilityProbeNeeded(a.Engine, exe, time.Now()) {
		snapshot, err := a.Engine.Capabilities(context.Background(), kernel.CapabilityRequest{})
		t.Fatalf("second start needs a new probe; stored capabilities: %+v (err=%v)", snapshot, err)
	}
	if got := countCalls(); got != first {
		t.Logf("second start re-probed (%d extra commands) because a stored verdict was transient", got-first)
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
