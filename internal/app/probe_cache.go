package app

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/engine"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

// capabilityCacheTTL is how long probe results stored by an earlier process
// are reused at startup. `sfs doctor` and capability requests with probe=true
// always probe.
const capabilityCacheTTL = time.Hour

// capabilityProbeNeeded reports whether Build must probe the providers. A
// full lark-cli probe starts about 35 subprocesses, so a process may instead
// reuse the results stored in the session database when every registered
// provider has a result younger than capabilityCacheTTL, every recorded failure
// is a compatibility verdict (unsupported or version_incompatible) rather than
// a transient one, and the lark-cli executable has not changed since.
func capabilityProbeNeeded(eng *engine.Engine, larkExecutable string, now time.Time) bool {
	snapshot, err := eng.Capabilities(context.Background(), kernel.CapabilityRequest{})
	if err != nil || len(snapshot.Providers) == 0 {
		return true
	}
	var oldest time.Time
	usesLarkCLI := false
	for _, capability := range snapshot.Providers {
		observed := capability.ObservedAt
		if observed == nil || observed.After(now) || now.Sub(*observed) > capabilityCacheTTL {
			return true
		}
		if !compatibilityVerdict(capability.Error) {
			return true
		}
		for _, operation := range capability.Operations {
			if !compatibilityVerdict(operation.Error) {
				return true
			}
		}
		if oldest.IsZero() || observed.Before(oldest) {
			oldest = *observed
		}
		if capability.Descriptor.Backend == "lark-cli" {
			usesLarkCLI = true
		}
	}
	return usesLarkCLI && executableChangedSince(larkExecutable, oldest)
}

func compatibilityVerdict(detail *kernel.ErrorDetail) bool {
	return detail == nil || detail.Type == kernel.ErrUnsupported || detail.Type == kernel.ErrVersionIncompatible
}

// executableChangedSince reports whether the executable found on PATH, or the
// symlink that points to it, was modified after since. Package managers
// recreate the symlink on upgrade even when they keep the target's timestamp.
func executableChangedSince(name string, since time.Time) bool {
	path, err := exec.LookPath(name)
	if err != nil {
		return true
	}
	for _, stat := range []func(string) (os.FileInfo, error){os.Lstat, os.Stat} {
		info, err := stat(path)
		if err != nil || info.ModTime().After(since) {
			return true
		}
	}
	return false
}
