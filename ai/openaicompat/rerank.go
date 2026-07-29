package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type Reranker struct {
	Client          *Client
	Model           string
	TopN            int
	MaxOutputTokens int
}

func (Reranker) Name() string { return "ai" }

func (r Reranker) Rerank(ctx context.Context, query string, candidates []kernel.Candidate) ([]kernel.Candidate, error) {
	limit := r.TopN
	if limit <= 0 || limit > 30 {
		limit = 30
	}
	if len(candidates) < limit {
		limit = len(candidates)
	}
	if limit < 2 {
		return append([]kernel.Candidate(nil), candidates...), nil
	}
	type item struct {
		ID      string            `json:"id"`
		Source  kernel.SourceID   `json:"source"`
		Kind    kernel.ObjectKind `json:"kind"`
		Title   string            `json:"title"`
		Snippet string            `json:"snippet"`
	}
	items := make([]item, 0, limit)
	byID := map[string]kernel.Candidate{}
	for _, candidate := range candidates[:limit] {
		id := candidate.Ref.CanonicalID
		if id == "" {
			id = candidate.Ref.NativeID
		}
		items = append(items, item{ID: id, Source: candidate.Source, Kind: candidate.Kind, Title: candidate.Title, Snippet: candidate.Snippet})
		byID[id] = candidate
	}
	input, _ := json.Marshal(map[string]any{"query": query, "candidates": items})
	schema := map[string]any{"type": "object", "required": []string{"ordered_ids"}, "properties": map[string]any{"ordered_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true}}, "additionalProperties": false}
	raw, err := r.Client.CompleteJSON(ctx, r.Model, "按与查询的语义相关性重新排列候选。只使用输入中的 id，每个 id 最多一次。", string(input), "rerank", schema, r.MaxOutputTokens)
	if err != nil {
		return nil, err
	}
	var response struct {
		OrderedIDs []string `json:"ordered_ids"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	out := make([]kernel.Candidate, 0, len(candidates))
	seen := map[string]bool{}
	for _, id := range response.OrderedIDs {
		candidate, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("reranker returned unknown candidate id %q", id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, candidate)
		}
	}
	for _, candidate := range candidates {
		id := candidate.Ref.CanonicalID
		if id == "" {
			id = candidate.Ref.NativeID
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, candidate)
		}
	}
	return out, nil
}
