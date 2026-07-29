package kernel

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ProjectionSet describes which portions of an object are materialized.
type ProjectionSet uint64

const (
	ProjectionHead ProjectionSet = 1 << iota
	ProjectionSnippet
	ProjectionSummary
	ProjectionStructure
	ProjectionContent
	ProjectionContext
	ProjectionRelations
	ProjectionAttachments
)

var projectionNames = []struct {
	Name string
	Bit  ProjectionSet
}{
	{"head", ProjectionHead},
	{"snippet", ProjectionSnippet},
	{"summary", ProjectionSummary},
	{"structure", ProjectionStructure},
	{"content", ProjectionContent},
	{"context", ProjectionContext},
	{"relations", ProjectionRelations},
	{"attachments", ProjectionAttachments},
}

func ParseProjectionSet(values []string) (ProjectionSet, error) {
	var out ProjectionSet
	for _, raw := range values {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		found := false
		for _, item := range projectionNames {
			if v == item.Name {
				out |= item.Bit
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown projection %q", raw)
		}
	}
	return out, nil
}

func (p ProjectionSet) Has(bits ProjectionSet) bool                 { return p&bits == bits }
func (p ProjectionSet) Any(bits ProjectionSet) bool                 { return p&bits != 0 }
func (p ProjectionSet) Union(other ProjectionSet) ProjectionSet     { return p | other }
func (p ProjectionSet) Intersect(other ProjectionSet) ProjectionSet { return p & other }
func (p ProjectionSet) Missing(have ProjectionSet) ProjectionSet    { return p &^ have }
func (p ProjectionSet) Empty() bool                                 { return p == 0 }

func (p ProjectionSet) Strings() []string {
	out := make([]string, 0, len(projectionNames))
	for _, item := range projectionNames {
		if p.Any(item.Bit) {
			out = append(out, item.Name)
		}
	}
	return out
}

func (p ProjectionSet) String() string { return strings.Join(p.Strings(), ",") }

func (p ProjectionSet) MarshalJSON() ([]byte, error) { return json.Marshal(p.Strings()) }

func (p *ProjectionSet) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = 0
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err == nil {
		parsed, parseErr := ParseProjectionSet(values)
		if parseErr != nil {
			return parseErr
		}
		*p = parsed
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return fmt.Errorf("projection must be a string or string array: %w", err)
	}
	parts := strings.Split(one, ",")
	sort.Strings(parts)
	parsed, err := ParseProjectionSet(parts)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}
