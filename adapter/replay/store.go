package replay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

const formatVersion = 1

type Entry struct {
	Version    int                 `json:"version"`
	ProviderID kernel.ProviderID   `json:"provider_id"`
	Operation  kernel.Operation    `json:"operation"`
	RequestKey string              `json:"request_key"`
	Request    json.RawMessage     `json:"request"`
	Response   json.RawMessage     `json:"response,omitempty"`
	Error      *kernel.ErrorDetail `json:"error,omitempty"`
	RecordedAt time.Time           `json:"recorded_at"`
}

type manifest struct {
	Version   int                         `json:"version"`
	Providers []kernel.ProviderDescriptor `json:"providers"`
}

type Store struct {
	dir         string
	mu          sync.Mutex
	descriptors map[kernel.ProviderID]kernel.ProviderDescriptor
}

func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("replay directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := validateReplayDirectory(dir); err != nil {
		return nil, err
	}
	entriesDir := filepath.Join(dir, "entries")
	if err := os.MkdirAll(entriesDir, 0o700); err != nil {
		return nil, err
	}
	if err := validateReplayDirectory(entriesDir); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, descriptors: map[kernel.ProviderID]kernel.ProviderDescriptor{}}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := rejectReplaySymlink(manifestPath, "replay manifest"); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(manifestPath); err == nil {
		m, err := decodeManifest(b)
		if err != nil {
			return nil, fmt.Errorf("decode replay manifest %q: %w", manifestPath, err)
		}
		for _, descriptor := range m.Providers {
			s.descriptors[descriptor.ID] = descriptor
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func validateReplayDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("replay directory %q must not be a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("replay path %q is not a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("replay directory %q is writable by group or other users", path)
	}
	return nil
}

func rejectReplaySymlink(path, label string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	return nil
}

func decodeManifest(raw []byte) (manifest, error) {
	var value manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifest{}, errors.New("manifest contains multiple JSON values")
		}
		return manifest{}, err
	}
	if value.Version != formatVersion {
		return manifest{}, fmt.Errorf("unsupported manifest version %d", value.Version)
	}
	seen := map[kernel.ProviderID]bool{}
	for _, descriptor := range value.Providers {
		if strings.TrimSpace(string(descriptor.ID)) == "" || descriptor.Source == "" {
			return manifest{}, errors.New("manifest provider requires id and source")
		}
		if seen[descriptor.ID] {
			return manifest{}, fmt.Errorf("manifest contains duplicate provider %q", descriptor.ID)
		}
		seen[descriptor.ID] = true
	}
	return value, nil
}

func (s *Store) Register(descriptor kernel.ProviderDescriptor) error {
	if descriptor.ID == "" {
		return fmt.Errorf("provider descriptor id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.descriptors[descriptor.ID] = descriptor
	return s.writeManifestLocked()
}

func (s *Store) Descriptors() []kernel.ProviderDescriptor {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]kernel.ProviderDescriptor, 0, len(s.descriptors))
	for _, descriptor := range s.descriptors {
		out = append(out, kernel.CloneJSON(descriptor))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) Save(providerID kernel.ProviderID, operation kernel.Operation, request, response any, callErr error) error {
	requestJSON, err := canonicalJSON(request)
	if err != nil {
		return fmt.Errorf("encode replay request: %w", err)
	}
	var responseJSON []byte
	if response != nil {
		responseJSON, err = json.Marshal(response)
		if err != nil {
			return fmt.Errorf("encode replay response: %w", err)
		}
	}
	entry := Entry{Version: formatVersion, ProviderID: providerID, Operation: operation, RequestKey: requestKey(providerID, operation, requestJSON), Request: requestJSON, Response: responseJSON, RecordedAt: time.Now().UTC()}
	if callErr != nil {
		entry.Error = kernel.DetailFromError(callErr)
	}
	b, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	path := s.entryPath(providerID, operation, entry.RequestKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicWrite(path, b, 0o600)
}

func (s *Store) Load(providerID kernel.ProviderID, operation kernel.Operation, request any, out any) error {
	requestJSON, err := canonicalJSON(request)
	if err != nil {
		return err
	}
	key := requestKey(providerID, operation, requestJSON)
	path := s.entryPath(providerID, operation, key)
	if err := rejectReplaySymlink(path, "replay entry"); err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: providerID, Message: err.Error()}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &kernel.ErrorDetail{Type: kernel.ErrNotFound, ProviderID: providerID, Message: "replay fixture not found", Details: map[string]any{"operation": operation, "request_key": key}}
		}
		return err
	}
	entry, err := decodeEntry(b)
	if err != nil {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: providerID, Message: "invalid replay fixture: " + err.Error()}
	}
	if entry.Version != formatVersion {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: providerID, Message: fmt.Sprintf("invalid replay fixture version %d", entry.Version)}
	}
	if entry.ProviderID != providerID || entry.Operation != operation || entry.RequestKey != key {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: providerID, Message: "replay fixture identity does not match its lookup path"}
	}
	entryRequest, err := canonicalJSON(entry.Request)
	if err != nil || !bytes.Equal(entryRequest, requestJSON) {
		return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: providerID, Message: "replay fixture request does not match the lookup request"}
	}
	if out != nil && len(entry.Response) > 0 && string(entry.Response) != "null" {
		if err := json.Unmarshal(entry.Response, out); err != nil {
			return &kernel.ErrorDetail{Type: kernel.ErrParse, ProviderID: providerID, Message: "invalid replay response: " + err.Error()}
		}
	}
	if entry.Error != nil {
		copy := *entry.Error
		return &copy
	}
	return nil
}

func decodeEntry(raw []byte) (Entry, error) {
	var entry Entry
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return Entry{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Entry{}, errors.New("fixture contains multiple JSON values")
		}
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) entryPath(providerID kernel.ProviderID, operation kernel.Operation, key string) string {
	return filepath.Join(s.dir, "entries", safe(string(providerID)), safe(string(operation)), key+".json")
}

func (s *Store) writeManifestLocked() error {
	providers := make([]kernel.ProviderDescriptor, 0, len(s.descriptors))
	for _, descriptor := range s.descriptors {
		providers = append(providers, descriptor)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	b, err := json.MarshalIndent(manifest{Version: formatVersion, Providers: providers}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, "manifest.json"), b, 0o600)
}

func canonicalJSON(value any) ([]byte, error) {
	value = normalizeReplayRequest(value)
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

// normalizeReplayRequest removes execution-local fields that must not take part
// in fixture identity. A recorded provider call can therefore be replayed from
// a different RetrievalSession while tenant/profile identity still remains in
// the key.
func normalizeReplayRequest(value any) any {
	switch v := value.(type) {
	case kernel.ProviderSearchRequest:
		v.SessionID = ""
		return v
	case *kernel.ProviderSearchRequest:
		if v == nil {
			return v
		}
		copy := *v
		copy.SessionID = ""
		return copy
	case kernel.ProviderQueryRequest:
		v.SessionID = ""
		return v
	case *kernel.ProviderQueryRequest:
		if v == nil {
			return v
		}
		copy := *v
		copy.SessionID = ""
		return copy
	case kernel.ProviderFetchRequest:
		v.SessionID = ""
		return v
	case []kernel.ProviderFetchRequest:
		out := append([]kernel.ProviderFetchRequest(nil), v...)
		for i := range out {
			out[i].SessionID = ""
		}
		return out
	case kernel.ProviderExpandRequest:
		v.SessionID = ""
		return v
	case *kernel.ProviderExpandRequest:
		if v == nil {
			return v
		}
		copy := *v
		copy.SessionID = ""
		return copy
	default:
		return value
	}
}
func requestKey(providerID kernel.ProviderID, operation kernel.Operation, canonical []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(providerID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(operation))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)[:16])
}
func safe(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, value)
	if value == "" {
		return "unknown"
	}
	return value
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
