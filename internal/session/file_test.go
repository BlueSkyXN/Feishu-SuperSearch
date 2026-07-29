package session

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

func TestFileStorePersistsSession(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Create(ctx, "scope")
	if err != nil {
		t.Fatal(err)
	}
	r.WithLock(func(x *Record) {
		x.Candidates["c1"] = kernel.Candidate{Ref: kernel.ObjectRef{CanonicalID: "c1"}, Title: "hello"}
	})
	if err := s.Save(ctx, r); err != nil {
		t.Fatal(err)
	}
	s2, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s2.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot(false); len(got.Candidates) != 1 || got.Candidates[0].Title != "hello" {
		t.Fatalf("restored=%+v", got)
	}
}

func TestFileStoreRejectsInvalidSessionPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), "../../outside"); err == nil {
		t.Fatal("expected invalid session id error")
	}
}

func TestFileStoreTTLRemovesExpiredJSON(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(context.Background(), "scope")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, record.ID+".json")
	time.Sleep(60 * time.Millisecond)
	listed, err := store.List(context.Background())
	if err != nil || len(listed) != 0 {
		t.Fatalf("listed=%v err=%v", listed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired session file still exists: %v", err)
	}
}

func TestFileStoreReportsCorruptSessionAtStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rs_"+strings.Repeat("a", 20)+".json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(dir, time.Hour); err == nil {
		t.Fatal("corrupt session was silently ignored")
	}
}

func TestFileStoreRejectsSharedWritableDirectoryAndSessionSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions and symlinks are not portable on Windows")
	}
	t.Run("shared writable directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		if _, err := NewFileStore(dir, time.Hour); err == nil || !strings.Contains(err.Error(), "writable by group or other users") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("session symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte(`{"id":"foreign"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "rs_"+strings.Repeat("b", 20)+".json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewFileStore(dir, time.Hour); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestFileStoreSaveDoesNotFollowLegacyFixedTempSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are not portable on Windows")
	}
	dir := t.TempDir()
	store, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.memory.Create(context.Background(), "scope")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	const sentinel = "do-not-overwrite"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyTemp := filepath.Join(dir, record.ID+".json.tmp")
	if err := os.Symlink(target, legacyTemp); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != sentinel {
		t.Fatalf("target=%q err=%v", content, err)
	}
}
