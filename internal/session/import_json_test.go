package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestImportJSONDirIsIdempotentAndReportsInvalidFiles(t *testing.T) {
	ctx := context.Background()
	sourceDir := t.TempDir()
	store, _ := newSQLiteTestStore(t, time.Hour)

	valid := NewRecord("import-scope", time.Hour)
	valid.WithLock(func(r *Record) {
		r.Candidates["imported"] = kernel.Candidate{
			Ref:   kernel.ObjectRef{CanonicalID: "imported"},
			Title: "imported candidate",
		}
	})
	validPath := filepath.Join(sourceDir, valid.ID+".json")
	validBytes, err := json.MarshalIndent(valid.Snapshot(true), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validPath, validBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	expired := NewRecord("expired-scope", time.Hour)
	expired.WithLock(func(r *Record) {
		r.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
		r.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	})
	expiredPath := filepath.Join(sourceDir, expired.ID+".json")
	expiredBytes, err := json.Marshal(expired.Snapshot(true))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expiredPath, expiredBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	invalidPath := filepath.Join(sourceDir, "invalid.json")
	invalidBytes := []byte(`{"id":`)
	if err := os.WriteFile(invalidPath, invalidBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := ImportJSONDir(ctx, sourceDir, store)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 3 || first.Imported != 1 || first.Invalid != 1 || first.Expired != 1 || first.Skipped != 2 || first.AlreadyPresent != 0 {
		t.Fatalf("first report = %+v", first)
	}
	if len(first.Issues) != 2 {
		t.Fatalf("first issues = %+v", first.Issues)
	}
	restored, err := store.Get(ctx, valid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(false).Candidates; len(got) != 1 || got[0].Title != "imported candidate" {
		t.Fatalf("imported candidates = %+v", got)
	}

	second, err := ImportJSONDir(ctx, sourceDir, store)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 3 || second.Imported != 0 || second.AlreadyPresent != 1 || second.Invalid != 1 || second.Expired != 1 || second.Skipped != 3 {
		t.Fatalf("second report = %+v", second)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("session count after repeated import = %d, want 1", len(list))
	}

	for path, want := range map[string][]byte{
		validPath:   validBytes,
		expiredPath: expiredBytes,
		invalidPath: invalidBytes,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("source file %q was removed: %v", path, err)
		}
		if string(got) != string(want) {
			t.Fatalf("source file %q was modified", path)
		}
	}
}
