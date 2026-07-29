package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestSQLiteStoreCRUD(t *testing.T) {
	ctx := context.Background()
	store, _ := newSQLiteTestStore(t, time.Hour)

	record, err := store.Create(ctx, "scope-a")
	if err != nil {
		t.Fatal(err)
	}
	record.WithLock(func(r *Record) {
		r.Candidates["candidate-1"] = kernel.Candidate{
			Ref:   kernel.ObjectRef{CanonicalID: "candidate-1"},
			Title: "persisted candidate",
		}
	})
	record.AddEvent(kernel.RetrievalEvent{Type: "test"})
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}

	restored, err := store.Get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot(true)
	if snapshot.ScopeKey != "scope-a" || len(snapshot.Candidates) != 1 || snapshot.Candidates[0].Title != "persisted candidate" {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
	if len(snapshot.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(snapshot.Events))
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != record.ID {
		t.Fatalf("list = %+v", list)
	}
	if len(list[0].Events) != 0 {
		t.Fatalf("List must omit events, got %d", len(list[0].Events))
	}

	if err := store.Delete(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, record.ID); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	if _, err := store.Get(ctx, record.ID); !isSessionNotFound(err) {
		t.Fatalf("Get after Delete error = %v, want not found", err)
	}
}

func TestSQLiteStoreRestoresAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(ctx, "restart-scope")
	if err != nil {
		t.Fatal(err)
	}
	record.WithLock(func(r *Record) {
		r.Artifacts["artifact-1"] = kernel.Artifact{
			Ref:    kernel.ObjectRef{CanonicalID: "artifact-1"},
			Chunks: []kernel.ContentChunk{{ID: "chunk-1", Text: "restart content"}},
		}
	})
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restored, err := reopened.Get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := restored.Snapshot(false)
	if len(snapshot.Artifacts) != 1 || len(snapshot.Artifacts[0].Chunks) != 1 || snapshot.Artifacts[0].Chunks[0].Text != "restart content" {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
}

func TestSQLiteStoreTTLDeletesExpiredRows(t *testing.T) {
	ctx := context.Background()
	store, _ := newSQLiteTestStore(t, 25*time.Millisecond)
	record, err := store.Create(ctx, "ttl-scope")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)

	if _, err := store.Get(ctx, record.ID); !isSessionNotFound(err) {
		t.Fatalf("expired Get error = %v, want not found", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", record.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired row count = %d, want 0", count)
	}
}

func TestSQLiteStoreConcurrentSaveAndGet(t *testing.T) {
	ctx := context.Background()
	store, _ := newSQLiteTestStore(t, time.Hour)
	record, err := store.Create(ctx, "concurrent-scope")
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	const iterations = 20
	errCh := make(chan error, workers*iterations*2)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				key := fmt.Sprintf("candidate-%d-%d", worker, iteration)
				record.WithLock(func(r *Record) {
					r.Candidates[key] = kernel.Candidate{Ref: kernel.ObjectRef{CanonicalID: key}, Title: key}
				})
				if err := store.Save(ctx, record); err != nil {
					errCh <- err
					return
				}
				if _, err := store.Get(ctx, record.ID); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent operation: %v", err)
	}
	if t.Failed() {
		return
	}
	if err := store.Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(restored.Snapshot(false).Candidates); got != workers*iterations {
		t.Fatalf("candidate count = %d, want %d", got, workers*iterations)
	}
}

func TestSQLiteStoreMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	for run := 0; run < 2; run++ {
		store, err := NewSQLiteStore(path, time.Hour)
		if err != nil {
			t.Fatalf("open run %d: %v", run, err)
		}
		var version int
		if err := store.db.QueryRowContext(ctx, "SELECT version FROM schema_version WHERE singleton = 1").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != currentSQLiteSchemaVersion {
			t.Fatalf("schema version = %d, want %d", version, currentSQLiteSchemaVersion)
		}
		var journalMode string
		if err := store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if journalMode != "wal" {
			t.Fatalf("journal_mode = %q, want wal", journalMode)
		}
		var busyTimeout int
		if err := store.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if busyTimeout != int(defaultSQLiteBusyTimeout.Milliseconds()) {
			t.Fatalf("busy_timeout = %d", busyTimeout)
		}
		var indexCount int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'index'
			  AND name IN ('sessions_expires_at_idx', 'sessions_scope_created_at_idx')`).Scan(&indexCount); err != nil {
			t.Fatal(err)
		}
		if indexCount != 2 {
			t.Fatalf("session index count = %d, want 2", indexCount)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteStorePersistsCapabilitiesAndQueryHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	generated := time.Now().UTC().Truncate(time.Nanosecond)
	capabilities := kernel.CapabilitySnapshot{GeneratedAt: generated, Providers: []kernel.ProviderCapability{{Descriptor: kernel.ProviderDescriptor{ID: "openapi.docs", Source: kernel.SourceDocs}, Status: kernel.StatusOK, Version: "test/1"}}}
	if err := store.SaveCapabilities(ctx, capabilities); err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(ctx, "scope-history")
	if err != nil {
		t.Fatal(err)
	}
	request := kernel.SearchRequest{Query: "项目延期", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages}, Identity: kernel.Identity{ScopeKey: "scope-history"}}
	if err := store.RecordQuery(ctx, record.ID, request); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordQuery(ctx, record.ID, request); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err := reopened.LoadCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].Descriptor.ID != "openapi.docs" || loaded.Providers[0].Version != "test/1" || !loaded.GeneratedAt.Equal(generated) {
		t.Fatalf("capabilities=%+v", loaded)
	}
	history, err := reopened.ListQueryHistory(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].SessionID != record.ID || history[0].Query != request.Query || len(history[0].Sources) != 2 {
		t.Fatalf("history=%+v", history)
	}
}

func TestSQLiteStoreCleansExpiredQueryHistory(t *testing.T) {
	ctx := context.Background()
	store, _ := newSQLiteTestStore(t, 20*time.Millisecond)
	record, err := store.Create(ctx, "scope-history")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordQuery(ctx, record.ID, kernel.SearchRequest{Query: "old", Identity: kernel.Identity{ScopeKey: "scope-history"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	history, err := store.ListQueryHistory(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("expired history=%+v", history)
	}
}

func TestSQLiteStoreRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := NewSQLiteStore(path, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE schema_version SET version = 999 WHERE singleton = 1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewSQLiteStore(path, time.Hour)
	var storeErr *SQLiteStoreError
	if !errors.As(err, &storeErr) || storeErr.Kind != SQLiteErrorMigration {
		t.Fatalf("error = %v, want structured migration error", err)
	}
}

func TestSQLiteMigrationRollsBackOnInterruptedSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE schema_version (singleton INTEGER PRIMARY KEY, version INTEGER NOT NULL)`,
		`INSERT INTO schema_version(singleton, version) VALUES (1, 1)`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, scope TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, snapshot_json BLOB NOT NULL)`,
		`CREATE TABLE query_history (id INTEGER PRIMARY KEY)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSQLiteStore(path, time.Hour); err == nil {
		t.Fatal("expected migration failure")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "SELECT version FROM schema_version WHERE singleton = 1").Scan(&version); err != nil {
		t.Fatal(err)
	}
	var capabilityTableCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='provider_capabilities'`).Scan(&capabilityTableCount); err != nil {
		t.Fatal(err)
	}
	if version != 1 || capabilityTableCount != 0 {
		t.Fatalf("migration was not rolled back: version=%d capability_tables=%d", version, capabilityTableCount)
	}
}

func TestSQLiteStoreReportsCorruptDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.sqlite")
	if err := os.WriteFile(path, []byte("this is not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewSQLiteStore(path, time.Hour)
	var storeErr *SQLiteStoreError
	if !errors.As(err, &storeErr) || storeErr.Kind != SQLiteErrorCorrupt {
		t.Fatalf("error = %v, want structured corrupt error", err)
	}
}

func TestSQLiteStoreReportsOpenError(t *testing.T) {
	path := t.TempDir()
	_, err := NewSQLiteStore(path, time.Hour)
	var storeErr *SQLiteStoreError
	if !errors.As(err, &storeErr) || storeErr.Kind != SQLiteErrorOpen {
		t.Fatalf("error = %v, want structured open error", err)
	}
}

func TestSQLiteStoreKeepsDatabaseAndSidecarsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	for _, test := range []struct {
		name string
		dsn  func(string) string
	}{
		{name: "plain path", dsn: func(path string) string { return path }},
		{name: "file URI", dsn: func(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() + "?cache=shared" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "sessions.sqlite")
			store, err := NewSQLiteStore(test.dsn(path), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.Create(context.Background(), "scope"); err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
				info, err := os.Stat(candidate)
				if errors.Is(err, os.ErrNotExist) && candidate != path {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != 0o600 {
					t.Fatalf("%s mode=%#o want 0600", candidate, got)
				}
			}
		})
	}
}

func TestSQLiteStoreRejectsSharedWritableDirectoryAndDatabaseSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions and symlinks are not portable on Windows")
	}
	t.Run("shared writable directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		_, err := NewSQLiteStore(filepath.Join(dir, "sessions.sqlite"), time.Hour)
		if err == nil || !strings.Contains(err.Error(), "writable by group or other users") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("database symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		const sentinel = "do-not-overwrite"
		if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "sessions.sqlite")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSQLiteStore(link, time.Hour); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("error=%v", err)
		}
		content, err := os.ReadFile(target)
		if err != nil || string(content) != sentinel {
			t.Fatalf("target=%q err=%v", content, err)
		}
	})
}

func TestSQLiteMemoryModeDetectionUsesExactQueryParameter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.sqlite")
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?label=mode%3Dmemory"
	if isMemorySQLitePath(dsn) {
		t.Fatalf("disk URI was misclassified as memory: %s", dsn)
	}
	resolved, err := sqliteFilesystemPath(dsn)
	if err != nil || resolved != path {
		t.Fatalf("resolved=%q err=%v want=%q", resolved, err, path)
	}
}

func newSQLiteTestStore(t *testing.T, ttl time.Duration) (*SQLiteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := NewSQLiteStore(path, ttl)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}
