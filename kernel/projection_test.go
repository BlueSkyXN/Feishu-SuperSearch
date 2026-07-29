package kernel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestProjectionJSONRoundTrip(t *testing.T) {
	want := ProjectionHead | ProjectionSummary | ProjectionContent
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got ProjectionSet
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
	if !reflect.DeepEqual(got.Strings(), []string{"head", "summary", "content"}) {
		t.Fatalf("unexpected strings: %v", got.Strings())
	}
}

func TestProjectionRejectsUnknown(t *testing.T) {
	var p ProjectionSet
	if err := json.Unmarshal([]byte(`["head","bogus"]`), &p); err == nil {
		t.Fatal("expected error")
	}
}

func TestSearchProfilesApplyDeterministicDefaults(t *testing.T) {
	fast := (SearchRequest{Query: "x", Strategy: SearchStrategy{Profile: "fast"}}).WithDefaults()
	if fast.Limit != 25 || fast.LimitPerSource != 5 || fast.Budget.DeadlineMS != 4000 || fast.Budget.MaxPagesPerSource != 1 || fast.Strategy.Pagination != "none" {
		t.Fatalf("fast defaults=%+v", fast)
	}
	balanced := (SearchRequest{Query: "x"}).WithDefaults()
	if balanced.Limit != 40 || balanced.LimitPerSource != 8 || balanced.Budget.DeadlineMS != 8000 || balanced.Budget.MaxPagesPerSource != 2 || !balanced.Strategy.SourceQuota {
		t.Fatalf("balanced defaults=%+v", balanced)
	}
	deep := (SearchRequest{Query: "x", Strategy: SearchStrategy{Profile: "deep"}}).WithDefaults()
	if deep.Limit != 60 || deep.LimitPerSource != 10 || deep.Budget.DeadlineMS != 15000 || deep.Budget.MaxFetches != 20 {
		t.Fatalf("deep defaults=%+v", deep)
	}
	noQuota := (SearchRequest{Query: "x", Strategy: SearchStrategy{DisableSourceQuota: true}}).WithDefaults()
	if noQuota.Strategy.SourceQuota {
		t.Fatal("source quota should be disabled")
	}
}

func TestProvenanceRawRefIsNotSerialized(t *testing.T) {
	raw, err := json.Marshal(Provenance{ProviderID: "test", RawRef: map[string]any{"secret": "must-not-leak"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "must-not-leak") || strings.Contains(string(raw), "raw_ref") {
		t.Fatalf("raw provider payload leaked: %s", raw)
	}
}
