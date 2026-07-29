package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

// FileStore is a dependency-free local persistence store. It uses one atomic
// JSON file per session. The Store interface allows replacing it with SQLite.
type FileStore struct {
	memory *MemoryStore
	dir    string
	mu     sync.Mutex
}

func NewFileStore(dir string, ttl time.Duration) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("session directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := validatePrivateStoreDirectory(dir); err != nil {
		return nil, err
	}
	s := &FileStore{memory: NewMemoryStore(ttl), dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read session directory %q: %w", dir, err)
	}
	for _, ent := range entries {
		if ent.IsDir() || filepath.Ext(ent.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(ent.Name(), ".json")
		if !sessionIDPattern.MatchString(id) {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		if ent.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("session file %q must not be a symlink", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read session file %q: %w", path, err)
		}
		var snap kernel.SessionSnapshot
		if err := json.Unmarshal(b, &snap); err != nil {
			return nil, fmt.Errorf("decode session file %q: %w", path, err)
		}
		if snap.ID != id {
			return nil, fmt.Errorf("session file %q contains id %q", path, snap.ID)
		}
		if !time.Now().Before(snap.ExpiresAt) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove expired session file %q: %w", path, err)
			}
			continue
		}
		r := Restore(snap)
		s.memory.records[r.ID] = r
	}
	return s, nil
}

var sessionIDPattern = regexp.MustCompile(`^rs_[a-f0-9]{20}$`)

func (s *FileStore) path(id string) (string, error) {
	if !sessionIDPattern.MatchString(id) {
		return "", &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid session id"}
	}
	return filepath.Join(s.dir, id+".json"), nil
}
func (s *FileStore) Create(ctx context.Context, scope string) (*Record, error) {
	r, err := s.memory.Create(ctx, scope)
	if err != nil {
		return nil, err
	}
	return r, s.Save(ctx, r)
}
func (s *FileStore) Get(ctx context.Context, id string) (*Record, error) {
	record, err := s.memory.Get(ctx, id)
	if err != nil {
		var detail *kernel.ErrorDetail
		if errors.As(err, &detail) && detail.Type == kernel.ErrNotFound {
			if path, pathErr := s.path(id); pathErr == nil {
				_ = os.Remove(path)
			}
		}
	}
	return record, err
}
func (s *FileStore) Save(ctx context.Context, r *Record) error {
	if err := s.memory.Save(ctx, r); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(r.Snapshot(true), "", "  ")
	if err != nil {
		return err
	}
	path, err := s.path(r.ID)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+r.ID+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
func (s *FileStore) Delete(ctx context.Context, id string) error {
	_ = s.memory.Delete(ctx, id)
	path, pathErr := s.path(id)
	if pathErr != nil {
		return pathErr
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (s *FileStore) List(ctx context.Context) ([]kernel.SessionSnapshot, error) {
	for _, id := range s.memory.cleanupExpired(time.Now()) {
		path, err := s.path(id)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return s.memory.List(ctx)
}
func (s *FileStore) Close() error { return nil }
