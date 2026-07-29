package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type CandidatePack struct {
	Query      string             `json:"query"`
	SessionID  string             `json:"session_id"`
	Candidates []kernel.Candidate `json:"candidates"`
	Sources    []kernel.SourceRun `json:"sources"`
}

type ArtifactPack struct {
	SessionID string            `json:"session_id"`
	Artifacts []kernel.Artifact `json:"artifacts"`
	Relations []kernel.Relation `json:"relations,omitempty"`
}

type Evidence struct {
	ID         string               `json:"id"`
	Kind       string               `json:"kind"`
	ClaimType  string               `json:"claim_type"`
	Text       string               `json:"text"`
	Quote      string               `json:"quote"`
	SourceRef  kernel.ObjectRef     `json:"source_ref"`
	Projection kernel.ProjectionSet `json:"projection"`
	SourceSpan SourceSpan           `json:"source_span"`
	Timestamp  *time.Time           `json:"timestamp,omitempty"`
	Confidence float64              `json:"confidence"`
	URL        string               `json:"url,omitempty"`
}

type SourceSpan struct {
	ChunkID string `json:"chunk_id,omitempty"`
	Start   int    `json:"start,omitempty"`
	End     int    `json:"end,omitempty"`
}

type EvidencePack struct {
	Query     string     `json:"query"`
	SessionID string     `json:"session_id"`
	Evidence  []Evidence `json:"evidence"`
}

type ResearchResult struct {
	CandidatePack CandidatePack `json:"candidate_pack"`
	ArtifactPack  ArtifactPack  `json:"artifact_pack"`
	EvidencePack  EvidencePack  `json:"evidence_pack"`
	Summary       string        `json:"summary"`
}

func Candidates(snapshot kernel.SearchSnapshot) CandidatePack {
	return CandidatePack{Query: snapshot.Query, SessionID: snapshot.SessionID, Candidates: snapshot.Candidates, Sources: snapshot.Sources}
}
func Artifacts(sessionID string, batch kernel.ArtifactBatch, relations []kernel.Relation) ArtifactPack {
	out := ArtifactPack{SessionID: sessionID, Relations: relations}
	for _, it := range batch.Items {
		if it.Artifact != nil {
			out.Artifacts = append(out.Artifacts, *it.Artifact)
		}
	}
	return out
}
func ExtractEvidence(query string, pack ArtifactPack) EvidencePack {
	out := EvidencePack{Query: query, SessionID: pack.SessionID}
	seen := map[string]bool{}
	appendEvidence := func(e Evidence) {
		e.Text = strings.TrimSpace(e.Text)
		if e.Text == "" {
			return
		}
		e.Quote = e.Text
		e.ID = stableEvidenceID(e)
		if seen[e.ID] {
			return
		}
		seen[e.ID] = true
		out.Evidence = append(out.Evidence, e)
	}
	for _, a := range pack.Artifacts {
		if a.Summary != nil && strings.TrimSpace(a.Summary.Text) != "" {
			appendEvidence(Evidence{Kind: "native_summary", ClaimType: "summary", Text: a.Summary.Text, SourceRef: a.Ref, Projection: kernel.ProjectionSummary, SourceSpan: SourceSpan{ChunkID: "native_summary"}, Confidence: .95, URL: a.Ref.URL})
		}
		for i, todo := range summaryTodos(a) {
			appendEvidence(Evidence{Kind: "todo", ClaimType: "action_item", Text: todo, SourceRef: a.Ref, Projection: kernel.ProjectionSummary, SourceSpan: SourceSpan{ChunkID: fmt.Sprintf("todo_%d", i+1)}, Confidence: .9, URL: a.Ref.URL})
		}
		for _, c := range a.Chunks {
			appendEvidence(Evidence{Kind: c.Kind, ClaimType: claimTypeForChunk(c.Kind), Text: c.Text, SourceRef: a.Ref, Projection: a.Projection, SourceSpan: SourceSpan{ChunkID: c.ID, Start: c.Start, End: c.End}, Timestamp: c.Timestamp, Confidence: .85, URL: a.Ref.URL})
		}
	}
	return out
}
func Summarize(pack EvidencePack) string {
	if len(pack.Evidence) == 0 {
		return "未获取到可用于总结的正文或原生摘要。"
	}
	groups := map[string][]Evidence{}
	for _, e := range pack.Evidence {
		groups[string(e.SourceRef.Kind)] = append(groups[string(e.SourceRef.Kind)], e)
	}
	order := []string{"minute", "document", "message", "task", "meeting", "event", "mail", "person", "chat", "unknown"}
	var b strings.Builder
	b.WriteString("基于已读取内容，检索到以下证据：\n")
	idx := 1
	for _, kind := range order {
		items := groups[kind]
		for _, e := range items {
			txt := strings.Join(strings.Fields(e.Text), " ")
			r := []rune(txt)
			if len(r) > 280 {
				txt = string(r[:280]) + "…"
			}
			b.WriteString("\n")
			b.WriteString(intString(idx))
			b.WriteString(". [")
			b.WriteString(kind)
			b.WriteString("] ")
			b.WriteString(txt)
			if e.URL != "" {
				b.WriteString(" （")
				b.WriteString(e.URL)
				b.WriteString("）")
			}
			idx++
		}
	}
	return b.String()
}
func summaryTodos(a kernel.Artifact) []string {
	if a.Summary == nil {
		return nil
	}
	out := []string{}
	for _, t := range a.Summary.Todos {
		parts := []string{}
		for _, k := range []string{"text", "title", "assignee", "due", "completed"} {
			if v, ok := t[k]; ok {
				parts = append(parts, k+"="+toString(v))
			}
		}
		if len(parts) > 0 {
			out = append(out, strings.Join(parts, ", "))
		}
	}
	sort.Strings(out)
	return out
}
func stableEvidenceID(e Evidence) string {
	key := strings.Join([]string{e.SourceRef.CanonicalID, e.SourceRef.NativeID, e.Kind, e.SourceSpan.ChunkID, fmt.Sprint(e.SourceSpan.Start), fmt.Sprint(e.SourceSpan.End), e.Text}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return "ev_" + hex.EncodeToString(sum[:10])
}

func claimTypeForChunk(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "todo", "task", "action_item":
		return "action_item"
	case "decision":
		return "decision"
	case "summary", "abstract":
		return "summary"
	default:
		return "fact"
	}
}
func intString(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append(buf, digits[n%10])
		n /= 10
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
func toString(v any) string {
	return strings.Join(strings.Fields(fmt.Sprint(v)), " ")
}
