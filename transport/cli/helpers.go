package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type StringList []string

func (s *StringList) String() string     { return strings.Join(*s, ",") }
func (s *StringList) Set(v string) error { *s = append(*s, v); return nil }

func ParseSources(value string) []kernel.SourceID {
	out := []kernel.SourceID{}
	seen := map[kernel.SourceID]bool{}
	for _, x := range strings.Split(value, ",") {
		s := kernel.SourceID(strings.TrimSpace(x))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func ParseProjection(value string) (kernel.ProjectionSet, error) {
	if strings.TrimSpace(value) == "" {
		return kernel.ProjectionContent, nil
	}
	var out kernel.ProjectionSet
	for _, raw := range strings.Split(value, ",") {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "head":
			out |= kernel.ProjectionHead
		case "snippet":
			out |= kernel.ProjectionSnippet
		case "summary":
			out |= kernel.ProjectionSummary
		case "structure":
			out |= kernel.ProjectionStructure
		case "content":
			out |= kernel.ProjectionContent
		case "context":
			out |= kernel.ProjectionContext
		case "relations":
			out |= kernel.ProjectionRelations
		case "attachments":
			out |= kernel.ProjectionAttachments
		case "":
		default:
			return 0, fmt.Errorf("unknown projection %q", raw)
		}
	}
	return out, nil
}
func ParseTime(value string, now time.Time) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if len(value) > 1 {
		unit := value[len(value)-1]
		n, err := strconv.Atoi(value[:len(value)-1])
		if err == nil {
			var d time.Duration
			switch unit {
			case 'm':
				d = time.Duration(n) * time.Minute
			case 'h':
				d = time.Duration(n) * time.Hour
			case 'd':
				d = time.Duration(n) * 24 * time.Hour
			case 'w':
				d = time.Duration(n) * 7 * 24 * time.Hour
			}
			if d != 0 {
				t := now.Add(-d)
				return &t, nil
			}
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("invalid time %q; use RFC3339, YYYY-MM-DD, 24h, 14d, or 2w", value)
}
func ParseIdentity(profile, mode string) kernel.Identity {
	return kernel.Identity{Profile: profile, Mode: kernel.IdentityMode(mode)}.Normalized()
}
func ReadJSON(r io.Reader, out any) error {
	dec := json.NewDecoder(r)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}
func PrintJSON(w io.Writer, v any, pretty bool) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

func PrintSearch(w io.Writer, s kernel.SearchSnapshot) {
	fmt.Fprintf(w, "Session: %s\nQuery: %s\nResults: %d (raw %d, partial=%v, %d ms)\n\n", s.SessionID, s.Query, len(s.Candidates), s.Stats.RawCandidates, s.Partial, s.Stats.ElapsedMS)
	for i, c := range s.Candidates {
		title := strings.Join(strings.Fields(c.Title), " ")
		if title == "" {
			title = c.Ref.NativeID
		}
		r := []rune(title)
		if len(r) > 72 {
			title = string(r[:72]) + "…"
		}
		fmt.Fprintf(w, "%2d. %-9s %-10s %7.4f  %s\n", i+1, c.Source, c.Kind, c.FusedScore, title)
		if c.Snippet != "" {
			sn := strings.Join(strings.Fields(c.Snippet), " ")
			rr := []rune(sn)
			if len(rr) > 110 {
				sn = string(rr[:110]) + "…"
			}
			fmt.Fprintf(w, "    %s\n", sn)
		}
		fmt.Fprintf(w, "    id=%s\n", c.Ref.CanonicalID)
		if c.URL != "" {
			fmt.Fprintf(w, "    %s\n", c.URL)
		}
	}
	if len(s.Sources) > 0 {
		fmt.Fprintln(w, "\nSources:")
		for _, run := range s.Sources {
			fmt.Fprintf(w, "  %-10s %-18s hits=%d pages=%d time=%dms", run.Source, run.Status, run.HitCount, run.Pages, run.ElapsedMS)
			if run.Error != nil {
				fmt.Fprintf(w, " error=%s", run.Error.Message)
			}
			fmt.Fprintln(w)
		}
	}
}
