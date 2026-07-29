package fusion

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Config struct {
	K0       float64
	Weights  map[kernel.SourceID]float64
	Quotas   map[kernel.SourceID]int
	UseQuota bool
}

func DefaultWeights() map[kernel.SourceID]float64 {
	return map[kernel.SourceID]float64{
		kernel.SourceDocs: 1.20, kernel.SourceMessages: 1.20, kernel.SourceMinutes: 1.10,
		kernel.SourceTasks: 1.00, kernel.SourceMeetings: .95, kernel.SourceCalendar: .90,
		kernel.SourcePeople: .85, kernel.SourceChats: .80, kernel.SourceMail: 1.00,
		kernel.SourceBase: .90, kernel.SourceSheets: .90,
	}
}

func DefaultQuotas() map[kernel.SourceID]int {
	return map[kernel.SourceID]int{
		kernel.SourceDocs: 10, kernel.SourceMessages: 12, kernel.SourceMinutes: 6,
		kernel.SourceMeetings: 4, kernel.SourceTasks: 6, kernel.SourcePeople: 4,
		kernel.SourceChats: 4, kernel.SourceCalendar: 5, kernel.SourceMail: 6,
		kernel.SourceBase: 6, kernel.SourceSheets: 6,
	}
}

func Canonicalize(c kernel.Candidate, scope string) kernel.Candidate {
	if c.Kind == "" {
		c.Kind = kindFromSource(c.Source)
	}
	if c.Ref.Kind == "" {
		c.Ref.Kind = c.Kind
	}
	if c.Ref.Source == "" {
		c.Ref.Source = c.Source
	}
	if c.Ref.Platform == "" {
		c.Ref.Platform = "feishu"
	}
	if c.URL == "" {
		c.URL = c.Ref.URL
	}
	if c.Ref.URL == "" {
		c.Ref.URL = c.URL
	}
	native := strings.TrimSpace(c.Ref.NativeID)
	if native == "" {
		native = idFromURL(c.URL)
	}
	if native == "" {
		native = fingerprint(c)
	}
	// The caller identity is authoritative. Providers may return native IDs and
	// URLs, but they cannot select the identity namespace used for caching.
	c.Ref.ScopeKey = scope
	c.Ref.NativeID = native
	c.Ref.CanonicalID = kernel.BuildCanonicalID(scope, c.Ref.Kind, native)
	if c.NativeRank <= 0 {
		c.NativeRank = 1
	}
	if len(c.DiscoveredBy) == 0 {
		c.DiscoveredBy = []kernel.Discovery{{ProviderID: c.Ref.ProviderID, Source: c.Source, Rank: c.NativeRank, Score: c.NativeScore}}
	}
	return c
}

func kindFromSource(s kernel.SourceID) kernel.ObjectKind {
	switch s {
	case kernel.SourceDocs:
		return kernel.KindDocument
	case kernel.SourceMessages:
		return kernel.KindMessage
	case kernel.SourceChats:
		return kernel.KindChat
	case kernel.SourcePeople:
		return kernel.KindPerson
	case kernel.SourceMinutes:
		return kernel.KindMinute
	case kernel.SourceMeetings:
		return kernel.KindMeeting
	case kernel.SourceCalendar:
		return kernel.KindEvent
	case kernel.SourceTasks:
		return kernel.KindTask
	case kernel.SourceMail:
		return kernel.KindMail
	case kernel.SourceBase:
		return kernel.KindRecord
	case kernel.SourceSheets:
		return kernel.KindCell
	default:
		return kernel.KindUnknown
	}
}

func idFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func fingerprint(c kernel.Candidate) string {
	raw := strings.ToLower(strings.TrimSpace(string(c.Kind) + "|" + c.Title + "|" + c.Snippet))
	h := sha256.Sum256([]byte(raw))
	return "fp_" + hex.EncodeToString(h[:12])
}

func Merge(a, b kernel.Candidate) kernel.Candidate {
	out := a
	if len(b.Title) > len(out.Title) {
		out.Title = b.Title
	}
	if len(b.Snippet) > len(out.Snippet) {
		out.Snippet = b.Snippet
	}
	if out.URL == "" {
		out.URL = b.URL
		out.Ref.URL = b.Ref.URL
	}
	if out.Timestamp == nil || (b.Timestamp != nil && b.Timestamp.After(*out.Timestamp)) {
		out.Timestamp = b.Timestamp
	}
	if len(b.Actors) > len(out.Actors) {
		out.Actors = append([]kernel.Actor(nil), b.Actors...)
	}
	if out.Container == nil {
		out.Container = b.Container
	}
	out.Projection |= b.Projection
	out.AvailableProjection |= b.AvailableProjection
	out.RelationHints = mergeHints(out.RelationHints, b.RelationHints)
	out.DiscoveredBy = mergeDiscoveries(out.DiscoveredBy, b.DiscoveredBy)
	if out.Provenance.RawRef == nil {
		out.Provenance = b.Provenance
	}
	return out
}

func mergeHints(a, b []kernel.RelationHint) []kernel.RelationHint {
	seen := map[string]bool{}
	out := make([]kernel.RelationHint, 0, len(a)+len(b))
	for _, x := range append(append([]kernel.RelationHint{}, a...), b...) {
		k := x.Type + "|" + x.TargetID
		if !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}
func mergeDiscoveries(a, b []kernel.Discovery) []kernel.Discovery {
	best := map[string]kernel.Discovery{}
	for _, x := range append(append([]kernel.Discovery{}, a...), b...) {
		k := string(x.ProviderID) + "|" + string(x.Source)
		old, ok := best[k]
		if !ok || x.Rank < old.Rank {
			best[k] = x
		}
	}
	keys := make([]string, 0, len(best))
	for k := range best {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kernel.Discovery, 0, len(keys))
	for _, k := range keys {
		out = append(out, best[k])
	}
	return out
}

func Fuse(query string, all []kernel.Candidate, limit int, cfg Config, scope string) ([]kernel.Candidate, int) {
	if cfg.K0 <= 0 {
		cfg.K0 = 60
	}
	if cfg.Weights == nil {
		cfg.Weights = DefaultWeights()
	}
	if cfg.Quotas == nil {
		cfg.Quotas = DefaultQuotas()
	}
	merged := make(map[string]kernel.Candidate, len(all))
	for _, raw := range all {
		c := Canonicalize(raw, scope)
		if prev, ok := merged[c.Ref.CanonicalID]; ok {
			merged[c.Ref.CanonicalID] = Merge(prev, c)
		} else {
			merged[c.Ref.CanonicalID] = c
		}
	}
	q := strings.ToLower(strings.TrimSpace(query))
	items := make([]kernel.Candidate, 0, len(merged))
	for _, c := range merged {
		score := 0.0
		for _, d := range c.DiscoveredBy {
			w := cfg.Weights[d.Source]
			if w == 0 {
				w = 1
			}
			rank := d.Rank
			if rank <= 0 {
				rank = 1
			}
			score += w / (cfg.K0 + float64(rank))
		}
		title := strings.ToLower(c.Title)
		snippet := strings.ToLower(c.Snippet)
		if q != "" && title == q {
			score += .05
		} else if q != "" && strings.Contains(title, q) {
			score += .025
		}
		if q != "" && strings.Contains(snippet, q) {
			score += .008
		}
		if len(c.DiscoveredBy) > 1 {
			score += math.Min(.015, .004*float64(len(c.DiscoveredBy)-1))
		}
		if c.Timestamp != nil {
			days := time.Since(*c.Timestamp).Hours() / 24
			if days >= 0 && days < 30 {
				score += .006 * (1 - days/30)
			}
		}
		c.FusedScore = score
		items = append(items, c)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].FusedScore == items[j].FusedScore {
			if items[i].Timestamp != nil && items[j].Timestamp != nil && !items[i].Timestamp.Equal(*items[j].Timestamp) {
				return items[i].Timestamp.After(*items[j].Timestamp)
			}
			return items[i].Ref.CanonicalID < items[j].Ref.CanonicalID
		}
		return items[i].FusedScore > items[j].FusedScore
	})
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	if !cfg.UseQuota {
		return append([]kernel.Candidate(nil), items[:limit]...), len(merged)
	}
	selected := make([]kernel.Candidate, 0, limit)
	counts := map[kernel.SourceID]int{}
	deferred := make([]kernel.Candidate, 0)
	for _, c := range items {
		quota := cfg.Quotas[c.Source]
		if quota <= 0 {
			quota = limit
		}
		if counts[c.Source] < quota && len(selected) < limit {
			selected = append(selected, c)
			counts[c.Source]++
		} else {
			deferred = append(deferred, c)
		}
	}
	for _, c := range deferred {
		if len(selected) >= limit {
			break
		}
		selected = append(selected, c)
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].FusedScore > selected[j].FusedScore })
	return selected, len(merged)
}
