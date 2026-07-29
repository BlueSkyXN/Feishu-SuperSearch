package planvalidate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	apiassets "github.com/BlueSkyXN/Feishu-SuperSearch/api"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaURL = "https://superfeishu.dev/schemas/retrieval-plan-v1.json"

var (
	schemaOnce sync.Once
	planSchema *jsonschema.Schema
	schemaErr  error
)

// ValidateJSON validates the original JSON representation against the embedded
// draft 2020-12 schema, then applies graph and operation-specific semantics.
// Schema validation intentionally happens before decoding to kernel structs so
// unknown fields cannot be silently discarded by encoding/json.
func ValidateJSON(raw []byte) error {
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid retrieval plan JSON: %w", err)
	}
	if err := validateSchema(instance); err != nil {
		return err
	}

	var plan kernel.RetrievalPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return fmt.Errorf("decode retrieval plan: %w", err)
	}
	return validateSemantics(plan)
}

// Validate validates a typed plan against the same schema and semantic rules
// used for untrusted JSON input.
func Validate(plan kernel.RetrievalPlan) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode retrieval plan: %w", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode encoded retrieval plan: %w", err)
	}
	if err := validateSchema(instance); err != nil {
		return err
	}
	return validateSemantics(plan)
}

func validateSchema(instance any) error {
	schemaOnce.Do(func() {
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(apiassets.RetrievalPlanSchema()))
		if err != nil {
			schemaErr = fmt.Errorf("decode embedded retrieval plan schema: %w", err)
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		if err := compiler.AddResource(schemaURL, document); err != nil {
			schemaErr = fmt.Errorf("register embedded retrieval plan schema: %w", err)
			return
		}
		planSchema, schemaErr = compiler.Compile(schemaURL)
		if schemaErr != nil {
			schemaErr = fmt.Errorf("compile embedded retrieval plan schema: %w", schemaErr)
		}
	})
	if schemaErr != nil {
		return schemaErr
	}
	if err := planSchema.Validate(instance); err != nil {
		return fmt.Errorf("retrieval plan schema validation failed: %w", err)
	}
	return nil
}

func validateSemantics(plan kernel.RetrievalPlan) error {
	nodes := make(map[string]kernel.PlanNode, len(plan.Nodes))
	for i, node := range plan.Nodes {
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("retrieval plan semantic validation failed: duplicate node %q", node.ID)
		}
		nodes[node.ID] = node
		if err := validateNodeRequest(node); err != nil {
			return fmt.Errorf("retrieval plan semantic validation failed: node %q: %w", node.ID, err)
		}
		for j, dependency := range node.DependsOn {
			if dependency == node.ID {
				return fmt.Errorf("retrieval plan semantic validation failed: node %q depends on itself", node.ID)
			}
			if strings.TrimSpace(dependency) == "" {
				return fmt.Errorf("retrieval plan semantic validation failed: node %q has empty dependency at index %d", node.ID, j)
			}
		}
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("retrieval plan semantic validation failed: node at index %d has an empty id", i)
		}
	}

	for _, node := range plan.Nodes {
		for _, dependency := range node.DependsOn {
			if _, exists := nodes[dependency]; !exists {
				return fmt.Errorf("retrieval plan semantic validation failed: node %q depends on unknown node %q", node.ID, dependency)
			}
		}
	}
	for _, output := range plan.Output {
		if _, exists := nodes[output]; !exists {
			return fmt.Errorf("retrieval plan semantic validation failed: output references unknown node %q", output)
		}
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	states := make(map[string]int, len(nodes))
	var visit func(string) error
	visit = func(id string) error {
		switch states[id] {
		case visiting:
			return fmt.Errorf("retrieval plan semantic validation failed: dependency cycle contains node %q", id)
		case visited:
			return nil
		}
		states[id] = visiting
		for _, dependency := range nodes[id].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		states[id] = visited
		return nil
	}
	for _, node := range plan.Nodes {
		if states[node.ID] == unvisited {
			if err := visit(node.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateNodeRequest(node kernel.PlanNode) error {
	switch node.Op {
	case kernel.PlanSearch:
		var request struct {
			Source         kernel.SourceID            `json:"source"`
			Query          string                     `json:"query"`
			SourceQueries  map[kernel.SourceID]string `json:"source_queries"`
			Sources        []kernel.SourceID          `json:"sources"`
			Filters        kernel.SearchFilters       `json:"filters"`
			Identity       kernel.Identity            `json:"identity"`
			Limit          int                        `json:"limit"`
			LimitPerSource int                        `json:"limit_per_source"`
			Budget         kernel.SearchBudget        `json:"budget"`
			Strategy       kernel.SearchStrategy      `json:"strategy"`
			SessionID      string                     `json:"session_id"`
		}
		if err := decodeNodeRequest(node, true, &request); err != nil {
			return err
		}
		if strings.TrimSpace(request.Query) == "" {
			return fmt.Errorf("search request requires a non-empty query")
		}
		if request.Source != "" && len(request.Sources) > 0 {
			return fmt.Errorf("search request must not set both source and sources")
		}
		search := kernel.SearchRequest{
			Query:          request.Query,
			SourceQueries:  request.SourceQueries,
			Sources:        request.Sources,
			Filters:        request.Filters,
			Limit:          request.Limit,
			LimitPerSource: request.LimitPerSource,
			Strategy:       request.Strategy,
		}.WithDefaults()
		if err := search.Validate(); err != nil {
			return fmt.Errorf("invalid search request: %w", err)
		}
	case kernel.PlanQuery:
		var request kernel.QueryRequest
		if err := decodeNodeRequest(node, true, &request); err != nil {
			return err
		}
		if request.Source == "" {
			return fmt.Errorf("query request requires source")
		}
		if request.Limit < 0 {
			return fmt.Errorf("query request limit must not be negative")
		}
		if request.Container != nil {
			if err := request.Container.Validate(); err != nil {
				return fmt.Errorf("invalid query container: %w", err)
			}
		}
	case kernel.PlanFetch:
		var request kernel.FetchBatchRequest
		if err := decodeNodeRequest(node, true, &request); err != nil {
			return err
		}
		if len(request.Items) == 0 {
			return fmt.Errorf("fetch request requires at least one item")
		}
		for i, item := range request.Items {
			if err := item.Ref.Validate(); err != nil {
				return fmt.Errorf("invalid fetch item %d: %w", i, err)
			}
			if item.Projection.Empty() {
				return fmt.Errorf("fetch item %d requires a projection", i)
			}
		}
	case kernel.PlanExpand:
		var request kernel.ExpandRequest
		if err := decodeNodeRequest(node, true, &request); err != nil {
			return err
		}
		if err := request.Ref.Validate(); err != nil {
			return fmt.Errorf("invalid expand ref: %w", err)
		}
		if request.MaxDepth < 0 || request.MaxDepth > 2 {
			return fmt.Errorf("expand max_depth must be between 0 and 2")
		}
	case kernel.PlanResolve:
		var request kernel.ResolveRequest
		if err := decodeNodeRequest(node, true, &request); err != nil {
			return err
		}
		if strings.TrimSpace(request.Text) == "" {
			return fmt.Errorf("resolve request requires non-empty text")
		}
		if request.Limit < 0 {
			return fmt.Errorf("resolve request limit must not be negative")
		}
	case kernel.PlanRank:
		var request struct {
			Query       string                      `json:"query"`
			Limit       int                         `json:"limit"`
			K0          float64                     `json:"k0"`
			Weights     map[kernel.SourceID]float64 `json:"weights"`
			Quotas      map[kernel.SourceID]int     `json:"quotas"`
			SourceQuota bool                        `json:"source_quota"`
		}
		if err := decodeNodeRequest(node, false, &request); err != nil {
			return err
		}
		if request.Limit < 0 || request.K0 < 0 {
			return fmt.Errorf("rank limit and k0 must not be negative")
		}
		for source, value := range request.Weights {
			if value < 0 {
				return fmt.Errorf("rank weight for source %q must not be negative", source)
			}
		}
		for source, value := range request.Quotas {
			if value < 0 {
				return fmt.Errorf("rank quota for source %q must not be negative", source)
			}
		}
	case kernel.PlanLimit:
		var request struct {
			TopK int `json:"top_k"`
		}
		if err := decodeNodeRequest(node, false, &request); err != nil {
			return err
		}
		if request.TopK < 0 {
			return fmt.Errorf("limit top_k must not be negative")
		}
	case kernel.PlanMapFetch:
		var request struct {
			TopK              int                                        `json:"top_k"`
			MaxItems          int                                        `json:"max_items"`
			ProjectionByKind  map[kernel.ObjectKind]kernel.ProjectionSet `json:"projection_by_kind"`
			DefaultProjection kernel.ProjectionSet                       `json:"default_projection"`
		}
		if err := decodeNodeRequest(node, false, &request); err != nil {
			return err
		}
		if request.TopK < 0 || request.MaxItems < 0 {
			return fmt.Errorf("map_fetch top_k and max_items must not be negative")
		}
		if request.MaxItems > 100 || request.TopK > 100 {
			return fmt.Errorf("map_fetch top_k and max_items must not exceed 100")
		}
	case kernel.PlanMerge, kernel.PlanDedup, kernel.PlanProject:
		if err := requireEmptyRequest(node); err != nil {
			return err
		}
	default:
		// The JSON Schema rejects unknown operations. Keep this guard for typed
		// callers so semantic validation remains safe if the schema changes.
		return fmt.Errorf("unsupported operation %q", node.Op)
	}
	return nil
}

func decodeNodeRequest(node kernel.PlanNode, required bool, out any) error {
	raw := bytes.TrimSpace(node.Request)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		if required {
			return fmt.Errorf("%s request is required", node.Op)
		}
		return nil
	}
	if err := decodeStrict(raw, out); err != nil {
		return fmt.Errorf("invalid %s request shape: %w", node.Op, err)
	}
	return nil
}

func requireEmptyRequest(node kernel.PlanNode) error {
	raw := bytes.TrimSpace(node.Request)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var request map[string]json.RawMessage
	if err := decodeStrict(raw, &request); err != nil {
		return fmt.Errorf("invalid %s request shape: %w", node.Op, err)
	}
	if len(request) != 0 {
		return fmt.Errorf("%s does not accept request fields", node.Op)
	}
	return nil
}

func decodeStrict(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
