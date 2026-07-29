package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

// JSONImportIssue records a source file that was skipped without stopping the
// rest of the migration.
type JSONImportIssue struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Error string `json:"error"`
}

// JSONImportReport makes partial imports auditable. AlreadyPresent rows are
// deliberately not overwritten, so rerunning an import cannot replace a newer
// database snapshot with stale JSON.
type JSONImportReport struct {
	Scanned        int               `json:"scanned"`
	Imported       int               `json:"imported"`
	AlreadyPresent int               `json:"already_present"`
	Invalid        int               `json:"invalid"`
	Expired        int               `json:"expired"`
	Skipped        int               `json:"skipped"`
	Issues         []JSONImportIssue `json:"issues,omitempty"`
}

// ImportJSONDir imports the atomic per-session JSON files written by FileStore.
// It never modifies or deletes source files. Invalid and expired files are
// skipped and reported; directory and destination-store failures are returned.
func ImportJSONDir(ctx context.Context, dir string, store Store) (JSONImportReport, error) {
	var report JSONImportReport
	if store == nil {
		return report, errors.New("destination session store is required")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return report, fmt.Errorf("read JSON session directory %q: %w", dir, err)
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		report.Scanned++
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			report.Invalid++
			report.Skipped++
			report.Issues = append(report.Issues, JSONImportIssue{Path: path, Kind: "read", Error: err.Error()})
			continue
		}
		var snapshot kernel.SessionSnapshot
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			report.Invalid++
			report.Skipped++
			report.Issues = append(report.Issues, JSONImportIssue{Path: path, Kind: "invalid_json", Error: err.Error()})
			continue
		}
		if snapshot.ScopeKey == "" && snapshot.Request != nil {
			snapshot.ScopeKey = snapshot.Request.Identity.Normalized().ScopeKey
		}
		if err := validateImportedSnapshot(snapshot); err != nil {
			report.Invalid++
			report.Skipped++
			report.Issues = append(report.Issues, JSONImportIssue{Path: path, Kind: "invalid_snapshot", Error: err.Error()})
			continue
		}
		if !snapshot.ExpiresAt.After(now) {
			report.Expired++
			report.Skipped++
			report.Issues = append(report.Issues, JSONImportIssue{Path: path, Kind: "expired", Error: "session has expired"})
			continue
		}

		_, err = store.Get(ctx, snapshot.ID)
		switch {
		case err == nil:
			report.AlreadyPresent++
			report.Skipped++
			continue
		case isSessionNotFound(err):
			// Expected for the first import.
		case err != nil:
			return report, fmt.Errorf("check destination session %q: %w", snapshot.ID, err)
		}

		if err := store.Save(ctx, Restore(snapshot)); err != nil {
			return report, fmt.Errorf("import session %q from %q: %w", snapshot.ID, path, err)
		}
		report.Imported++
	}
	return report, nil
}

func validateImportedSnapshot(snapshot kernel.SessionSnapshot) error {
	if !sessionIDPattern.MatchString(snapshot.ID) {
		return errors.New("invalid or missing session id")
	}
	if snapshot.CreatedAt.IsZero() || snapshot.ExpiresAt.IsZero() {
		return errors.New("created_at and expires_at are required")
	}
	if snapshot.ScopeKey == "" {
		return errors.New("scope_key is required")
	}
	if !snapshot.ExpiresAt.After(snapshot.CreatedAt) {
		return errors.New("expires_at must be after created_at")
	}
	return nil
}

func isSessionNotFound(err error) bool {
	var detail *kernel.ErrorDetail
	return errors.As(err, &detail) && detail.Type == kernel.ErrNotFound
}
