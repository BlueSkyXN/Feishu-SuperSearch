package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"modernc.org/sqlite"
)

const (
	currentSQLiteSchemaVersion = 2
	defaultSQLiteBusyTimeout   = 5 * time.Second
)

// SQLiteErrorKind classifies persistent-store failures without requiring
// callers to parse driver-specific error strings.
type SQLiteErrorKind string

const (
	SQLiteErrorOpen      SQLiteErrorKind = "open"
	SQLiteErrorCorrupt   SQLiteErrorKind = "corrupt"
	SQLiteErrorMigration SQLiteErrorKind = "migration"
	SQLiteErrorRead      SQLiteErrorKind = "read"
	SQLiteErrorWrite     SQLiteErrorKind = "write"
	SQLiteErrorClose     SQLiteErrorKind = "close"
)

// SQLiteStoreError preserves the operation and database path for persistence
// diagnostics while retaining the original error through Unwrap.
type SQLiteStoreError struct {
	Kind SQLiteErrorKind
	Op   string
	Path string
	Err  error
}

func (e *SQLiteStoreError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("sqlite session store %s (%s) %q: %v", e.Op, e.Kind, e.Path, e.Err)
}

func (e *SQLiteStoreError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SQLiteStore persists complete SessionSnapshot JSON documents and keeps the
// fields used for identity scoping, ordering, and TTL cleanup in indexed
// columns. A single database/sql connection makes connection-local PRAGMAs
// deterministic; WAL still allows other processes to read concurrently.
type SQLiteStore struct {
	db             *sql.DB
	path           string
	filesystemPath string
	ttl            time.Duration

	closeOnce sync.Once
	closeErr  error
}

// NewSQLiteStore opens or creates a SQLite session database, applies all
// schema migrations transactionally, and removes expired rows. path may be a
// normal filesystem path, a SQLite file: URI, or :memory:.
func NewSQLiteStore(path string, ttl time.Duration) (*SQLiteStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, &SQLiteStoreError{Kind: SQLiteErrorOpen, Op: "validate_path", Path: path, Err: errors.New("database path is required")}
	}
	if ttl <= 0 {
		ttl = 45 * time.Minute
	}
	filesystemPath, err := prepareSQLitePath(path)
	if err != nil {
		return nil, wrapSQLiteError(SQLiteErrorOpen, "prepare_path", path, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, wrapSQLiteError(SQLiteErrorOpen, "open", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLiteStore{db: db, path: path, filesystemPath: filesystemPath, ttl: ttl}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.secureFiles(); err != nil {
		_ = db.Close()
		return nil, wrapSQLiteError(SQLiteErrorOpen, "secure_files", path, err)
	}
	return store, nil
}

func prepareSQLitePath(path string) (string, error) {
	filesystemPath, err := sqliteFilesystemPath(path)
	if err != nil || filesystemPath == "" {
		return filesystemPath, err
	}
	if err := rejectSymlink(filesystemPath, "SQLite database path"); err != nil {
		return "", err
	}
	if info, err := os.Stat(filesystemPath); err == nil && info.IsDir() {
		return "", errors.New("database path is a directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir := filepath.Dir(filesystemPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	if err := validatePrivateStoreDirectory(dir); err != nil {
		return "", err
	}
	file, err := os.OpenFile(filesystemPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	if closeErr := file.Close(); closeErr != nil {
		return "", closeErr
	}
	if err := os.Chmod(filesystemPath, 0o600); err != nil {
		return "", err
	}
	return filesystemPath, nil
}

func sqliteFilesystemPath(path string) (string, error) {
	if isMemorySQLitePath(path) {
		return "", nil
	}
	if !strings.HasPrefix(path, "file:") {
		return path, nil
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" || (parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost")) {
		return "", errors.New("SQLite file URI must use an empty or localhost authority")
	}
	value := parsed.Path
	if value == "" {
		value = parsed.Opaque
	}
	if value == "" {
		return "", errors.New("SQLite file URI has no filesystem path")
	}
	return value, nil
}

func (s *SQLiteStore) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return wrapSQLiteError(SQLiteErrorOpen, "ping", s.path, err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", defaultSQLiteBusyTimeout.Milliseconds())); err != nil {
		return wrapSQLiteError(SQLiteErrorOpen, "set_busy_timeout", s.path, err)
	}
	var journalMode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		return wrapSQLiteError(SQLiteErrorOpen, "enable_wal", s.path, err)
	}
	if !strings.EqualFold(journalMode, "wal") && !isMemorySQLitePath(s.path) {
		return &SQLiteStoreError{Kind: SQLiteErrorOpen, Op: "enable_wal", Path: s.path, Err: fmt.Errorf("journal mode is %q, want WAL", journalMode)}
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return wrapSQLiteError(SQLiteErrorOpen, "enable_foreign_keys", s.path, err)
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	if _, err := s.CleanupExpired(ctx); err != nil {
		return err
	}
	return nil
}

func isMemorySQLitePath(path string) bool {
	if path == ":memory:" || strings.EqualFold(path, "file::memory:") {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(path), "file:") {
		return false
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Query().Get("mode"), "memory") || strings.EqualFold(parsed.Opaque, ":memory:")
}

func (s *SQLiteStore) secureFiles() error {
	if s.filesystemPath == "" {
		return nil
	}
	for _, path := range []string{s.filesystemPath, s.filesystemPath + "-wal", s.filesystemPath + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapSQLiteError(SQLiteErrorMigration, "begin_migration", s.path, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_version (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			version INTEGER NOT NULL CHECK (version >= 0)
		)`); err != nil {
		return wrapSQLiteError(SQLiteErrorMigration, "create_schema_version", s.path, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_version(singleton, version) VALUES (1, 0)"); err != nil {
		return wrapSQLiteError(SQLiteErrorMigration, "initialize_schema_version", s.path, err)
	}

	var version int
	if err := tx.QueryRowContext(ctx, "SELECT version FROM schema_version WHERE singleton = 1").Scan(&version); err != nil {
		return wrapSQLiteError(SQLiteErrorMigration, "read_schema_version", s.path, err)
	}
	if version > currentSQLiteSchemaVersion {
		return &SQLiteStoreError{
			Kind: SQLiteErrorMigration,
			Op:   "check_schema_version",
			Path: s.path,
			Err:  fmt.Errorf("database schema version %d is newer than supported version %d", version, currentSQLiteSchemaVersion),
		}
	}

	for version < currentSQLiteSchemaVersion {
		next := version + 1
		for _, statement := range sqliteMigrationStatements(next) {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return wrapSQLiteError(SQLiteErrorMigration, fmt.Sprintf("apply_migration_%d", next), s.path, err)
			}
		}
		result, err := tx.ExecContext(ctx, "UPDATE schema_version SET version = ? WHERE singleton = 1", next)
		if err != nil {
			return wrapSQLiteError(SQLiteErrorMigration, fmt.Sprintf("record_migration_%d", next), s.path, err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err == nil {
				err = fmt.Errorf("updated %d schema version rows", affected)
			}
			return wrapSQLiteError(SQLiteErrorMigration, fmt.Sprintf("verify_migration_%d", next), s.path, err)
		}
		version = next
	}

	if err := tx.Commit(); err != nil {
		return wrapSQLiteError(SQLiteErrorMigration, "commit_migration", s.path, err)
	}
	committed = true
	return nil
}

func sqliteMigrationStatements(version int) []string {
	switch version {
	case 1:
		return []string{
			`CREATE TABLE IF NOT EXISTS sessions (
				id TEXT PRIMARY KEY,
				scope TEXT NOT NULL,
				created_at INTEGER NOT NULL,
				expires_at INTEGER NOT NULL,
				snapshot_json BLOB NOT NULL
			)`,
			"CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at)",
			"CREATE INDEX IF NOT EXISTS sessions_scope_created_at_idx ON sessions(scope, created_at DESC)",
		}
	case 2:
		return []string{
			`CREATE TABLE IF NOT EXISTS provider_capabilities (
				provider_id TEXT PRIMARY KEY,
				observed_at INTEGER NOT NULL,
				capability_json BLOB NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS query_history (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL,
				scope TEXT NOT NULL,
				query TEXT NOT NULL,
				sources_json BLOB NOT NULL,
				created_at INTEGER NOT NULL,
				expires_at INTEGER NOT NULL,
				UNIQUE(session_id, query)
			)`,
			"CREATE INDEX IF NOT EXISTS query_history_scope_created_at_idx ON query_history(scope, created_at DESC)",
			"CREATE INDEX IF NOT EXISTS query_history_expires_at_idx ON query_history(expires_at)",
		}
	default:
		return nil
	}
}

func (s *SQLiteStore) Create(ctx context.Context, scope string) (*Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := NewRecord(scope, s.ttl)
	if err := s.Save(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Record, error) {
	if !sessionIDPattern.MatchString(id) {
		return nil, &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid session id"}
	}
	var (
		scope      string
		createdAt  int64
		expiresAt  int64
		snapshotJS []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT scope, created_at, expires_at, snapshot_json
		FROM sessions
		WHERE id = ?`, id).Scan(&scope, &createdAt, &expiresAt, &snapshotJS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sessionNotFound("session not found")
	}
	if err != nil {
		return nil, wrapSQLiteError(SQLiteErrorRead, "get", s.path, err)
	}
	if expiresAt <= time.Now().UTC().UnixNano() {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id); err != nil {
			return nil, wrapSQLiteError(SQLiteErrorWrite, "delete_expired", s.path, err)
		}
		return nil, sessionNotFound("session expired")
	}

	snapshot, err := decodeStoredSnapshot(id, scope, createdAt, expiresAt, snapshotJS)
	if err != nil {
		return nil, wrapSQLiteError(SQLiteErrorCorrupt, "decode_snapshot", s.path, err)
	}
	return Restore(snapshot), nil
}

func (s *SQLiteStore) Save(ctx context.Context, r *Record) error {
	if r == nil {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "nil session"}
	}
	snapshot := r.Snapshot(true)
	if !sessionIDPattern.MatchString(snapshot.ID) {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid session id"}
	}
	if snapshot.CreatedAt.IsZero() || snapshot.ExpiresAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.CreatedAt) {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid session timestamps"}
	}
	if !snapshot.ExpiresAt.After(time.Now().UTC()) {
		return s.Delete(ctx, snapshot.ID)
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "encode_snapshot", s.path, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions(id, scope, created_at, expires_at, snapshot_json)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope = excluded.scope,
			created_at = excluded.created_at,
			expires_at = excluded.expires_at,
			snapshot_json = excluded.snapshot_json`,
		snapshot.ID,
		snapshot.ScopeKey,
		snapshot.CreatedAt.UTC().UnixNano(),
		snapshot.ExpiresAt.UTC().UnixNano(),
		b,
	)
	if err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "save", s.path, err)
	}
	if err := s.secureFiles(); err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "secure_save_files", s.path, err)
	}
	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	if !sessionIDPattern.MatchString(id) {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "invalid session id"}
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id); err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "delete", s.path, err)
	}
	if err := s.secureFiles(); err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "secure_delete_files", s.path, err)
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context) ([]kernel.SessionSnapshot, error) {
	if _, err := s.CleanupExpired(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scope, created_at, expires_at, snapshot_json
		FROM sessions
		ORDER BY created_at DESC, id ASC`)
	if err != nil {
		return nil, wrapSQLiteError(SQLiteErrorRead, "list", s.path, err)
	}
	defer rows.Close()

	out := make([]kernel.SessionSnapshot, 0)
	for rows.Next() {
		var (
			id, scope  string
			createdAt  int64
			expiresAt  int64
			snapshotJS []byte
		)
		if err := rows.Scan(&id, &scope, &createdAt, &expiresAt, &snapshotJS); err != nil {
			return nil, wrapSQLiteError(SQLiteErrorRead, "scan_list", s.path, err)
		}
		snapshot, err := decodeStoredSnapshot(id, scope, createdAt, expiresAt, snapshotJS)
		if err != nil {
			return nil, wrapSQLiteError(SQLiteErrorCorrupt, "decode_list_snapshot", s.path, err)
		}
		snapshot.Events = nil
		out = append(out, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapSQLiteError(SQLiteErrorRead, "iterate_list", s.path, err)
	}
	return out, nil
}

// CleanupExpired deletes all sessions whose expiry is at or before the current
// UTC time and returns the number of removed rows.
func (s *SQLiteStore) CleanupExpired(ctx context.Context) (int64, error) {
	now := time.Now().UTC().UnixNano()
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", now)
	if err != nil {
		return 0, wrapSQLiteError(SQLiteErrorWrite, "cleanup_expired", s.path, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, wrapSQLiteError(SQLiteErrorWrite, "cleanup_expired_result", s.path, err)
	}
	historyResult, err := s.db.ExecContext(ctx, "DELETE FROM query_history WHERE expires_at <= ?", now)
	if err != nil {
		return count, wrapSQLiteError(SQLiteErrorWrite, "cleanup_expired_history", s.path, err)
	}
	historyCount, err := historyResult.RowsAffected()
	if err != nil {
		return count, wrapSQLiteError(SQLiteErrorWrite, "cleanup_expired_history_result", s.path, err)
	}
	if err := s.secureFiles(); err != nil {
		return count + historyCount, wrapSQLiteError(SQLiteErrorWrite, "secure_cleanup_files", s.path, err)
	}
	return count + historyCount, nil
}

func (s *SQLiteStore) SaveCapabilities(ctx context.Context, snapshot kernel.CapabilitySnapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "begin_save_capabilities", s.path, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	observed := snapshot.GeneratedAt.UTC()
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	for _, capability := range snapshot.Providers {
		if capability.Descriptor.ID == "" {
			return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "provider capability requires descriptor.id"}
		}
		raw, err := json.Marshal(capability)
		if err != nil {
			return wrapSQLiteError(SQLiteErrorWrite, "encode_capability", s.path, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_capabilities(provider_id, observed_at, capability_json)
			VALUES (?, ?, ?)
			ON CONFLICT(provider_id) DO UPDATE SET
				observed_at = excluded.observed_at,
				capability_json = excluded.capability_json`, capability.Descriptor.ID, observed.UnixNano(), raw); err != nil {
			return wrapSQLiteError(SQLiteErrorWrite, "save_capability", s.path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "commit_save_capabilities", s.path, err)
	}
	committed = true
	if err := s.secureFiles(); err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "secure_capability_files", s.path, err)
	}
	return nil
}

func (s *SQLiteStore) LoadCapabilities(ctx context.Context) (kernel.CapabilitySnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT observed_at, capability_json FROM provider_capabilities ORDER BY provider_id`)
	if err != nil {
		return kernel.CapabilitySnapshot{}, wrapSQLiteError(SQLiteErrorRead, "load_capabilities", s.path, err)
	}
	defer rows.Close()
	var snapshot kernel.CapabilitySnapshot
	for rows.Next() {
		var observed int64
		var raw []byte
		if err := rows.Scan(&observed, &raw); err != nil {
			return kernel.CapabilitySnapshot{}, wrapSQLiteError(SQLiteErrorRead, "scan_capability", s.path, err)
		}
		var capability kernel.ProviderCapability
		if err := json.Unmarshal(raw, &capability); err != nil || capability.Descriptor.ID == "" {
			if err == nil {
				err = errors.New("capability is missing descriptor.id")
			}
			return kernel.CapabilitySnapshot{}, wrapSQLiteError(SQLiteErrorCorrupt, "decode_capability", s.path, err)
		}
		snapshot.Providers = append(snapshot.Providers, capability)
		when := time.Unix(0, observed).UTC()
		if when.After(snapshot.GeneratedAt) {
			snapshot.GeneratedAt = when
		}
	}
	if err := rows.Err(); err != nil {
		return kernel.CapabilitySnapshot{}, wrapSQLiteError(SQLiteErrorRead, "iterate_capabilities", s.path, err)
	}
	return snapshot, nil
}

func (s *SQLiteStore) RecordQuery(ctx context.Context, sessionID string, request kernel.SearchRequest) error {
	if !sessionIDPattern.MatchString(sessionID) || strings.TrimSpace(request.Query) == "" {
		return &kernel.ErrorDetail{Type: kernel.ErrInvalidRequest, Message: "query history requires a valid session and query"}
	}
	sources, err := json.Marshal(request.Sources)
	if err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "encode_query_sources", s.path, err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO query_history(session_id, scope, query, sources_json, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, sessionID, request.Identity.Normalized().ScopeKey, request.Query, sources, now.UnixNano(), now.Add(s.ttl).UnixNano())
	if err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "record_query", s.path, err)
	}
	if err := s.secureFiles(); err != nil {
		return wrapSQLiteError(SQLiteErrorWrite, "secure_query_history_files", s.path, err)
	}
	return nil
}

func (s *SQLiteStore) ListQueryHistory(ctx context.Context, limit int) ([]QueryHistoryEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if _, err := s.CleanupExpired(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, scope, query, sources_json, created_at, expires_at
		FROM query_history ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, wrapSQLiteError(SQLiteErrorRead, "list_query_history", s.path, err)
	}
	defer rows.Close()
	var entries []QueryHistoryEntry
	for rows.Next() {
		var entry QueryHistoryEntry
		var sources []byte
		var created, expires int64
		if err := rows.Scan(&entry.ID, &entry.SessionID, &entry.ScopeKey, &entry.Query, &sources, &created, &expires); err != nil {
			return nil, wrapSQLiteError(SQLiteErrorRead, "scan_query_history", s.path, err)
		}
		if err := json.Unmarshal(sources, &entry.Sources); err != nil {
			return nil, wrapSQLiteError(SQLiteErrorCorrupt, "decode_query_history", s.path, err)
		}
		entry.CreatedAt = time.Unix(0, created).UTC()
		entry.ExpiresAt = time.Unix(0, expires).UTC()
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapSQLiteError(SQLiteErrorRead, "iterate_query_history", s.path, err)
	}
	return entries, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if err := s.db.Close(); err != nil {
			s.closeErr = wrapSQLiteError(SQLiteErrorClose, "close", s.path, err)
		}
	})
	return s.closeErr
}

func decodeStoredSnapshot(id, scope string, createdAt, expiresAt int64, raw []byte) (kernel.SessionSnapshot, error) {
	var snapshot kernel.SessionSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode session %q JSON: %w", id, err)
	}
	if snapshot.ID != id || snapshot.ScopeKey != scope || snapshot.CreatedAt.UTC().UnixNano() != createdAt || snapshot.ExpiresAt.UTC().UnixNano() != expiresAt {
		return snapshot, fmt.Errorf("session %q indexed columns do not match snapshot JSON", id)
	}
	return snapshot, nil
}

func sessionNotFound(message string) *kernel.ErrorDetail {
	return &kernel.ErrorDetail{Type: kernel.ErrNotFound, Message: message}
}

func wrapSQLiteError(kind SQLiteErrorKind, op, path string, err error) error {
	if err == nil {
		return nil
	}
	if isSQLiteCorruption(err) {
		kind = SQLiteErrorCorrupt
	}
	return &SQLiteStoreError{Kind: kind, Op: op, Path: path, Err: err}
}

func isSQLiteCorruption(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 11, 26: // SQLITE_CORRUPT, SQLITE_NOTADB
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database disk image is malformed") ||
		strings.Contains(message, "file is not a database")
}

var _ Store = (*SQLiteStore)(nil)
