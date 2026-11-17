package engine

import (
	"encoding/json"
	"maps"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

// Metadata and nested errors may be typed in-process or JSON maps after replay.
func detailValue(value any) *kernel.ErrorDetail {
	if detail, ok := value.(*kernel.ErrorDetail); ok {
		if detail == nil {
			return nil
		}
		copy := *detail
		return &copy
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var detail kernel.ErrorDetail
	if json.Unmarshal(raw, &detail) != nil || detail.Type == "" || detail.Message == "" {
		return nil
	}
	return &detail
}

func fetchObjectError(err error, ref kernel.ObjectRef) error {
	detail := kernel.DetailFromError(err)
	if detail == nil {
		return nil
	}
	if objects, ok := detail.Details["objects"].(map[string]any); ok {
		if item := detailValue(objects[ref.NativeID]); item != nil {
			return item
		}
	}
	return err
}

func pageEnrichmentError(page kernel.CandidatePage) *kernel.ErrorDetail {
	detail := detailValue(page.Metadata["enrichment_error"])
	if detail != nil {
		detail.Subtype = "partial_enrichment"
	}
	return detail
}

func pendingEnrichment(detail *kernel.ErrorDetail) *kernel.ErrorDetail {
	if detail == nil {
		return nil
	}
	if detail.Subtype == "partial_enrichment" {
		return detail
	}
	return detailValue(detail.Details["enrichment_error"])
}

// Retrying a failed page can clear its failure, but cannot fill missing details
// from a page already consumed. Retain that warning across later page outcomes.
func carryEnrichment(previous, current *kernel.ErrorDetail) *kernel.ErrorDetail {
	pending := pendingEnrichment(previous)
	if pending == nil {
		return current
	}
	if current == nil {
		return pending
	}
	copy := *current
	copy.Details = maps.Clone(current.Details)
	if copy.Details == nil {
		copy.Details = map[string]any{}
	}
	key := "enrichment_error"
	if current.Subtype == "partial_enrichment" {
		key = "previous_enrichment"
	}
	copy.Details[key] = pending
	return &copy
}
