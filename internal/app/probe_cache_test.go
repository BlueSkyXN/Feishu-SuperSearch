package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/session"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

// probeTestEnv is a larkcli-backed app fixture whose probe calls are logged
// to a file, so cache reuse can be asserted on actual subprocess counts.
type probeTestEnv struct {
	t     *testing.T
	dir   string
	calls string
	exe   string
	cfg   Config
}

func newProbeTestEnv(t *testing.T) *probeTestEnv {
	t.Helper()
	dir := t.TempDir()
	env := &probeTestEnv{t: t, dir: dir, calls: filepath.Join(dir, "calls.log"), exe: filepath.Join(dir, "lark-cli")}
	script := "#!/bin/sh\necho \"$*\" >> '" + env.calls + "'\nif [ \"$1\" = \"--version\" ]; then echo 1.0.0-test; fi\nexit 0\n"
	if err := os.WriteFile(env.exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(env.exe, past, past); err != nil {
		t.Fatal(err)
	}
	env.cfg = DefaultConfig()
	env.cfg.Backend = "larkcli"
	env.cfg.LarkCLI.Executable = env.exe
	env.cfg.Storage = StorageConfig{Type: "sqlite", Path: filepath.Join(dir, "sfs.db"), TTL: "45m"}
	return env
}

func (e *probeTestEnv) build() *App {
	e.t.Helper()
	a, err := Build(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = a.Close() })
	return a
}

func (e *probeTestEnv) countCalls() int {
	e.t.Helper()
	raw, err := os.ReadFile(e.calls)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Count(string(raw), "\n")
}

// storedCapabilities reads the persisted capability snapshot through the
// actual CapabilityStore (LoadCapabilities). It must never go through Build
// or Capabilities: Build consults the cache and may re-probe, which would
// both consume (and repair) the very first-cache evidence the reuse test
// asserts on and skew the subprocess counters.
func (e *probeTestEnv) storedCapabilities(t *testing.T) kernel.CapabilitySnapshot {
	t.Helper()
	store, err := session.NewSQLiteStore(e.cfg.Storage.Path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.LoadCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// failOnTransientVerdicts reports, with evidence, any capability stored by
// the first build that is not a settled compatibility verdict. A transient
// entry legitimately forces a re-probe, so the strict reuse assertion below
// is only valid once this returns nil.
func failOnTransientVerdicts(t *testing.T, snapshot kernel.CapabilitySnapshot) {
	t.Helper()
	for _, capability := range snapshot.Providers {
		if err := capability.Error; err != nil && !compatibilityVerdict(err) {
			t.Fatalf("first probe stored a transient verdict for %s (%s: %s); evidence: %+v — this environment cannot assert strict cache reuse", capability.Descriptor.ID, err.Type, err.Message, capability)
		}
		for operation, operationCapability := range capability.Operations {
			if err := operationCapability.Error; err != nil && !compatibilityVerdict(err) {
				t.Fatalf("first probe stored a transient verdict for %s/%s (%s: %s); evidence: %+v — this environment cannot assert strict cache reuse", capability.Descriptor.ID, operation, err.Type, err.Message, operationCapability)
			}
		}
	}
}

// TestStoredCapabilitiesReadsPersistedCacheWithoutProbing pins the read
// helper itself to LoadCapabilities: opening it must not run any probe
// subprocess, and a transient verdict injected into the persisted cache must
// survive the read untouched (it must not be consumed or repaired).
func TestStoredCapabilitiesReadsPersistedCacheWithoutProbing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX executable")
	}
	env := newProbeTestEnv(t)
	env.build()

	store, err := session.NewSQLiteStore(env.cfg.Storage.Path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.LoadCapabilities(context.Background())
	if err != nil || len(stored.Providers) == 0 {
		t.Fatalf("load err=%v providers=%d", err, len(stored.Providers))
	}
	stored.Providers[0].Error = &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Message: "injected first-cache transient"}
	if err := store.SaveCapabilities(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	before := env.countCalls()
	observed := env.storedCapabilities(t)
	after := env.countCalls()
	hasTransient := false
	for _, capability := range observed.Providers {
		if capability.Error != nil && capability.Error.Type == kernel.ErrUpstreamTransient {
			hasTransient = true
		}
	}
	if after != before {
		t.Fatalf("storedCapabilities ran %d probe commands; the read helper must go through LoadCapabilities only", after-before)
	}
	if !hasTransient {
		t.Fatal("storedCapabilities consumed or repaired the injected transient verdict")
	}
}

func TestBuildReusesStoredProbeUntilExecutableChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX executable")
	}
	env := newProbeTestEnv(t)

	env.build()
	first := env.countCalls()
	if first == 0 {
		t.Fatal("first start should probe lark-cli")
	}
	// Strict reuse is only decidable when the stored cache is settled: read
	// the persisted snapshot (no Build, no probe) and require it to hold no
	// transient verdict, then demand the second Build make zero additional
	// probe calls.
	failOnTransientVerdicts(t, env.storedCapabilities(t))
	before := env.countCalls()
	a := env.build()
	if got := env.countCalls(); got != before {
		t.Fatalf("second start ran %d probe commands, want 0 (valid stored cache, unchanged executable)", got-before)
	}
	if capabilityProbeNeeded(a.Engine, env.exe, time.Now()) {
		t.Fatal("valid stored cache with unchanged executable must not need a probe")
	}
	if !capabilityProbeNeeded(a.Engine, env.exe, time.Now().Add(capabilityCacheTTL+time.Minute)) {
		t.Fatal("stored probe results older than the TTL must not be reused")
	}
	// Touching the executable must trigger probing again; compare against
	// the count right before this build, not against the first build.
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(env.exe, future, future); err != nil {
		t.Fatal(err)
	}
	before = env.countCalls()
	env.build()
	if env.countCalls() == before {
		t.Fatal("start after lark-cli changed should probe again")
	}
}

// TestBuildReprobesWhenStoredVerdictIsTransient verifies the transient branch
// deterministically: a stored capability carrying a transient error must make
// the next Build probe again, without relying on load-induced timeouts. The
// transient verdict is injected through the store and read back through the
// read-only helper first, so the helper provably did not consume it.
func TestBuildReprobesWhenStoredVerdictIsTransient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX executable")
	}
	env := newProbeTestEnv(t)

	// Build once to populate a valid cache, then overwrite one provider's
	// stored verdict with a transient error through the same store Build
	// uses.
	env.build()
	failOnTransientVerdicts(t, env.storedCapabilities(t))
	transient := env.storedCapabilities(t)
	for i := range transient.Providers {
		if transient.Providers[i].Descriptor.Backend == "lark-cli" {
			transient.Providers[i].Error = &kernel.ErrorDetail{Type: kernel.ErrUpstreamTransient, Message: "injected transient verdict"}
			break
		}
	}
	store, err := session.NewSQLiteStore(env.cfg.Storage.Path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCapabilities(context.Background(), transient); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	before := env.countCalls()
	env.build()
	if env.countCalls() == before {
		t.Fatal("Build must re-probe when the stored cache holds a transient verdict")
	}
}
