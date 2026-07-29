package kernel

import "time"

type EventType string

const (
	EventSessionStarted   EventType = "session.started"
	EventNodeStarted      EventType = "node.started"
	EventSourceCompleted  EventType = "source.completed"
	EventCandidateUpsert  EventType = "candidate.upsert"
	EventArtifactUpsert   EventType = "artifact.upsert"
	EventNodeRetrying     EventType = "node.retrying"
	EventNodeCompleted    EventType = "node.completed"
	EventSessionPartial   EventType = "session.partial"
	EventSessionCompleted EventType = "session.completed"
	EventError            EventType = "error"
)

type RetrievalEvent struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id,omitempty"`
	Type      EventType      `json:"type"`
	Time      time.Time      `json:"time"`
	NodeID    string         `json:"node_id,omitempty"`
	Source    SourceID       `json:"source,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}
