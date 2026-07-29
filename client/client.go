// Package client provides a typed HTTP client for a SuperFeishuSearch server.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	"github.com/BlueSkyXN/Feishu-SuperSearch/synthesis"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type ResearchResult struct {
	Planner       string                  `json:"planner"`
	Plan          kernel.RetrievalPlan    `json:"plan"`
	PlanResult    kernel.PlanResult       `json:"plan_result"`
	CandidatePack synthesis.CandidatePack `json:"candidate_pack"`
	ArtifactPack  synthesis.ArtifactPack  `json:"artifact_pack"`
	EvidencePack  synthesis.EvidencePack  `json:"evidence_pack"`
	Summary       string                  `json:"summary"`
	Events        []kernel.RetrievalEvent `json:"events,omitempty"`
	Warnings      []string                `json:"warnings,omitempty"`
}

type AskResult struct {
	Research ResearchResult `json:"research"`
	Answer   ai.Answer      `json:"answer"`
	Answerer string         `json:"answerer"`
	Partial  bool           `json:"partial"`
	Warnings []string       `json:"warnings,omitempty"`
}

func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
func (c *Client) Search(ctx context.Context, r kernel.SearchRequest) (kernel.SearchSnapshot, error) {
	var out kernel.SearchSnapshot
	return out, c.do(ctx, http.MethodPost, "/v1/search", r, &out)
}
func (c *Client) Continue(ctx context.Context, r kernel.ContinueRequest) (kernel.SearchSnapshot, error) {
	var out kernel.SearchSnapshot
	return out, c.do(ctx, http.MethodPost, "/v1/search/continue", r, &out)
}
func (c *Client) Query(ctx context.Context, r kernel.QueryRequest) (kernel.QuerySnapshot, error) {
	var out kernel.QuerySnapshot
	return out, c.do(ctx, http.MethodPost, "/v1/query", r, &out)
}
func (c *Client) Fetch(ctx context.Context, r kernel.FetchBatchRequest) (kernel.ArtifactBatch, error) {
	var out kernel.ArtifactBatch
	return out, c.do(ctx, http.MethodPost, "/v1/fetch", r, &out)
}
func (c *Client) Expand(ctx context.Context, r kernel.ExpandRequest) (kernel.RelationBatch, error) {
	var out kernel.RelationBatch
	return out, c.do(ctx, http.MethodPost, "/v1/expand", r, &out)
}
func (c *Client) Resolve(ctx context.Context, r kernel.ResolveRequest) ([]kernel.ObjectRef, error) {
	var out struct {
		Refs []kernel.ObjectRef `json:"refs"`
	}
	return out.Refs, c.do(ctx, http.MethodPost, "/v1/resolve", r, &out)
}
func (c *Client) Capabilities(ctx context.Context, probe bool) (kernel.CapabilitySnapshot, error) {
	var out kernel.CapabilitySnapshot
	path := "/v1/capabilities"
	if probe {
		path += "?probe=true"
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
func (c *Client) Session(ctx context.Context, id string) (kernel.SessionSnapshot, error) {
	return c.SessionForIdentity(ctx, id, kernel.Identity{})
}
func (c *Client) SessionForIdentity(ctx context.Context, id string, identity kernel.Identity) (kernel.SessionSnapshot, error) {
	var out kernel.SessionSnapshot
	query := "?mode=" + url.QueryEscape(string(identity.Normalized().Mode))
	if identity.Profile != "" {
		query += "&profile=" + url.QueryEscape(identity.Profile)
	}
	return out, c.do(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id)+query, nil, &out)
}
func (c *Client) Research(ctx context.Context, r planner.UserRequest) (ResearchResult, error) {
	var out ResearchResult
	return out, c.do(ctx, http.MethodPost, "/v1/research", r, &out)
}
func (c *Client) Ask(ctx context.Context, r planner.UserRequest) (AskResult, error) {
	var out AskResult
	return out, c.do(ctx, http.MethodPost, "/v1/ask", r, &out)
}
func (c *Client) ExecutePlan(ctx context.Context, p kernel.RetrievalPlan) (kernel.PlanResult, []kernel.RetrievalEvent, error) {
	var out struct {
		Result kernel.PlanResult       `json:"result"`
		Events []kernel.RetrievalEvent `json:"events"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/plans:execute", p, &out)
	return out.Result, out.Events, err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("base URL is required")
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error *kernel.ErrorDetail `json:"error"`
		}
		if json.Unmarshal(payload, &envelope) == nil && envelope.Error != nil {
			return envelope.Error
		}
		return fmt.Errorf("sfs HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode sfs response: %w", err)
	}
	return nil
}
