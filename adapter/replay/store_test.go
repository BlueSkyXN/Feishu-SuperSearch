package replay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestReplayIgnoresSessionID(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req1 := kernel.ProviderSearchRequest{Query: "A 项目", Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "tenant-a"}, PageSize: 8, SessionID: "rs_recorded"}
	response := kernel.CandidatePage{Candidates: []kernel.Candidate{{Title: "result"}}, RawCount: 1}
	if err := store.Save("mock.docs", kernel.OpSearch, req1, response, nil); err != nil {
		t.Fatal(err)
	}

	req2 := req1
	req2.SessionID = "rs_replayed"
	var got kernel.CandidatePage
	if err := store.Load("mock.docs", kernel.OpSearch, req2, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Title != "result" {
		t.Fatalf("unexpected replay: %#v", got)
	}
}

func TestReplayKeepsScopeIdentityInKey(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := kernel.ProviderSearchRequest{Query: "A 项目", Identity: kernel.Identity{Mode: kernel.IdentityUser, ScopeKey: "tenant-a"}, PageSize: 8}
	if err := store.Save("mock.docs", kernel.OpSearch, req, kernel.CandidatePage{}, nil); err != nil {
		t.Fatal(err)
	}
	req.Identity.ScopeKey = "tenant-b"
	var got kernel.CandidatePage
	err = store.Load("mock.docs", kernel.OpSearch, req, &got)
	var detail *kernel.ErrorDetail
	if !errors.As(err, &detail) || detail.Type != kernel.ErrNotFound {
		t.Fatalf("expected replay miss, got %v", err)
	}
}

func TestReplayPersistsErrors(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := kernel.ProviderResolveRequest{Text: "张三", Identity: kernel.Identity{ScopeKey: "x"}}
	original := &kernel.ErrorDetail{Type: kernel.ErrMissingScope, Message: "scope missing", ProviderID: "p"}
	if err := store.Save("p", kernel.OpResolve, req, nil, original); err != nil {
		t.Fatal(err)
	}
	var out []kernel.ObjectRef
	err = store.Load("p", kernel.OpResolve, req, &out)
	var detail *kernel.ErrorDetail
	if !errors.As(err, &detail) || detail.Type != kernel.ErrMissingScope {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreManifestRegistrationAndReload(t *testing.T) {
	if _, err := NewStore("  "); err == nil {
		t.Fatal("blank replay directory accepted")
	}
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Register(kernel.ProviderDescriptor{}); err == nil {
		t.Fatal("empty provider id accepted")
	}
	docs := kernel.ProviderDescriptor{ID: "z.docs", Source: kernel.SourceDocs, ObjectKinds: []kernel.ObjectKind{kernel.KindDocument}}
	tasks := kernel.ProviderDescriptor{ID: "a.tasks", Source: kernel.SourceTasks}
	if err := store.Register(docs); err != nil {
		t.Fatal(err)
	}
	if err := store.Register(tasks); err != nil {
		t.Fatal(err)
	}
	descriptors := store.Descriptors()
	if len(descriptors) != 2 || descriptors[0].ID != tasks.ID || descriptors[1].ID != docs.ID {
		t.Fatalf("descriptors=%+v", descriptors)
	}
	descriptors[1].ObjectKinds[0] = kernel.KindTask
	if got := store.Descriptors()[1].ObjectKinds[0]; got != kernel.KindDocument {
		t.Fatalf("descriptor was not cloned: %s", got)
	}
	reopened, err := NewStore(dir)
	if err != nil || !reflect.DeepEqual(reopened.Descriptors(), store.Descriptors()) {
		t.Fatalf("reopened descriptors=%+v err=%v", reopened.Descriptors(), err)
	}

	invalidManifestDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidManifestDir, "manifest.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if invalidManifest, err := NewStore(invalidManifestDir); err == nil || invalidManifest != nil || !strings.Contains(err.Error(), "decode replay manifest") {
		t.Fatalf("invalid manifest store=%+v err=%v", invalidManifest, err)
	}
	wrongVersionDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wrongVersionDir, "manifest.json"), []byte(`{"version":2,"providers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(wrongVersionDir); err == nil || !strings.Contains(err.Error(), "unsupported manifest version") {
		t.Fatalf("wrong version error=%v", err)
	}

	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(filePath); err == nil {
		t.Fatal("file path accepted as replay directory")
	}
	manifestReadFailure := t.TempDir()
	if err := os.Mkdir(filepath.Join(manifestReadFailure, "manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(manifestReadFailure); err == nil {
		t.Fatal("unreadable manifest accepted")
	}
}

func TestStoreEncodingParseAndFilesystemErrors(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("p", kernel.OpSearch, func() {}, nil, nil); err == nil {
		t.Fatal("unencodable request accepted")
	}
	if err := store.Save("p", kernel.OpSearch, map[string]any{"q": "x"}, func() {}, nil); err == nil {
		t.Fatal("unencodable response accepted")
	}
	if err := store.Load("p", kernel.OpSearch, func() {}, nil); err == nil {
		t.Fatal("unencodable load request accepted")
	}

	req := kernel.ProviderSearchRequest{Query: "broken"}
	canonical, err := canonicalJSON(req)
	if err != nil {
		t.Fatal(err)
	}
	path := store.entryPath("p", kernel.OpSearch, requestKey("p", kernel.OpSearch, canonical))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	var page kernel.CandidatePage
	var detail *kernel.ErrorDetail
	if err := store.Load("p", kernel.OpSearch, req, &page); !errors.As(err, &detail) || detail.Type != kernel.ErrParse {
		t.Fatalf("invalid entry error=%v", err)
	}

	entry := Entry{Version: formatVersion, ProviderID: "p", Operation: kernel.OpSearch, RequestKey: requestKey("p", kernel.OpSearch, canonical), Request: canonical, Response: json.RawMessage(`"not-an-object"`)}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	detail = nil
	if err := store.Load("p", kernel.OpSearch, req, &page); !errors.As(err, &detail) || detail.Type != kernel.ErrParse {
		t.Fatalf("invalid response error=%v", err)
	}

	parentFile := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(parentFile, "child"), []byte("x"), 0o600); err == nil {
		t.Fatal("atomicWrite succeeded below a file")
	}
}

func TestReplayRejectsFixtureIdentityAndSchemaDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(Entry) []byte
	}{
		{name: "wrong version", mutate: func(entry Entry) []byte { entry.Version++; b, _ := json.Marshal(entry); return b }},
		{name: "wrong provider", mutate: func(entry Entry) []byte { entry.ProviderID = "other"; b, _ := json.Marshal(entry); return b }},
		{name: "wrong operation", mutate: func(entry Entry) []byte { entry.Operation = kernel.OpFetch; b, _ := json.Marshal(entry); return b }},
		{name: "wrong key", mutate: func(entry Entry) []byte { entry.RequestKey = "wrong"; b, _ := json.Marshal(entry); return b }},
		{name: "wrong request", mutate: func(entry Entry) []byte {
			entry.Request = json.RawMessage(`{"query":"other"}`)
			b, _ := json.Marshal(entry)
			return b
		}},
		{name: "unknown field", mutate: func(entry Entry) []byte {
			b, _ := json.Marshal(entry)
			return append(b[:len(b)-1], []byte(`,"unknown":true}`)...)
		}},
		{name: "trailing value", mutate: func(entry Entry) []byte { b, _ := json.Marshal(entry); return append(b, []byte(` {}`)...) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			request := kernel.ProviderSearchRequest{Query: "project", Identity: kernel.Identity{ScopeKey: "scope"}}
			if err := store.Save("p", kernel.OpSearch, request, kernel.CandidatePage{}, nil); err != nil {
				t.Fatal(err)
			}
			canonical, err := canonicalJSON(request)
			if err != nil {
				t.Fatal(err)
			}
			path := store.entryPath("p", kernel.OpSearch, requestKey("p", kernel.OpSearch, canonical))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var entry Entry
			if err := json.Unmarshal(raw, &entry); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, test.mutate(entry), 0o600); err != nil {
				t.Fatal(err)
			}
			var page kernel.CandidatePage
			var detail *kernel.ErrorDetail
			if err := store.Load("p", kernel.OpSearch, request, &page); !errors.As(err, &detail) || detail.Type != kernel.ErrParse {
				t.Fatalf("error=%v detail=%+v", err, detail)
			}
		})
	}
}

func TestReplayRejectsSharedWritableDirectoriesAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and symlinks are not portable on Windows")
	}
	t.Run("shared writable directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(dir); err == nil || !strings.Contains(err.Error(), "writable by group or other users") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("store directory symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "replay-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(link); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("manifest symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(target, []byte(`{"version":1,"providers":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "manifest.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(dir); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("entry symlink", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		request := kernel.ProviderSearchRequest{Query: "project"}
		if err := store.Save("p", kernel.OpSearch, request, kernel.CandidatePage{}, nil); err != nil {
			t.Fatal(err)
		}
		canonical, _ := canonicalJSON(request)
		path := store.entryPath("p", kernel.OpSearch, requestKey("p", kernel.OpSearch, canonical))
		target := filepath.Join(t.TempDir(), "entry.json")
		raw, err := os.ReadFile(path)
		if err != nil || os.WriteFile(target, raw, 0o600) != nil {
			t.Fatalf("copy fixture err=%v", err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		var page kernel.CandidatePage
		var detail *kernel.ErrorDetail
		if err := store.Load("p", kernel.OpSearch, request, &page); !errors.As(err, &detail) || detail.Type != kernel.ErrParse || !strings.Contains(detail.Message, "must not be a symlink") {
			t.Fatalf("error=%v detail=%+v", err, detail)
		}
	})
}

func TestReplayNormalizationAndSafePathComponents(t *testing.T) {
	search := kernel.ProviderSearchRequest{Query: "q", SessionID: "session"}
	query := kernel.ProviderQueryRequest{SessionID: "session"}
	fetch := kernel.ProviderFetchRequest{SessionID: "session"}
	expand := kernel.ProviderExpandRequest{SessionID: "session"}
	values := []any{
		search, &search,
		query, &query,
		fetch, []kernel.ProviderFetchRequest{fetch},
		expand, &expand,
		(*kernel.ProviderSearchRequest)(nil), (*kernel.ProviderQueryRequest)(nil), (*kernel.ProviderExpandRequest)(nil),
		"unchanged",
	}
	for _, value := range values {
		if _, err := canonicalJSON(value); err != nil {
			t.Fatalf("canonicalJSON(%T): %v", value, err)
		}
	}
	if got := normalizeReplayRequest(&search).(kernel.ProviderSearchRequest); got.SessionID != "" || search.SessionID != "session" {
		t.Fatalf("search normalization mutated input: got=%+v input=%+v", got, search)
	}
	if got := normalizeReplayRequest([]kernel.ProviderFetchRequest{fetch}).([]kernel.ProviderFetchRequest); got[0].SessionID != "" {
		t.Fatalf("fetch normalization=%+v", got)
	}
	if safe(" a/b:c ") != "a_b_c" || safe("***") != "___" || safe("  ") != "unknown" {
		t.Fatalf("safe values=%q %q %q", safe(" a/b:c "), safe("***"), safe("  "))
	}
}
