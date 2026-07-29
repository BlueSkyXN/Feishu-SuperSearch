package research

import (
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

type EventType string

const (
	EventProgress  EventType = "progress"
	EventRetrieval EventType = "retrieval"
)

type Phase string

const (
	PhaseCapabilities     Phase = "capabilities"
	PhasePlanning         Phase = "planning"
	PhaseRetrieving       Phase = "retrieving"
	PhaseReranking        Phase = "reranking"
	PhaseEvidence         Phase = "evidence"
	PhaseResearchComplete Phase = "research_complete"
	PhaseAnswering        Phase = "answering"
	PhaseComplete         Phase = "complete"
)

type Event struct {
	Type      EventType              `json:"type"`
	Phase     Phase                  `json:"phase"`
	Message   string                 `json:"message,omitempty"`
	Time      time.Time              `json:"time"`
	Retrieval *kernel.RetrievalEvent `json:"retrieval,omitempty"`
}

type Observer func(Event)

func progressEvent(phase Phase, message string) Event {
	return Event{Type: EventProgress, Phase: phase, Message: message, Time: time.Now().UTC()}
}

func retrievalEvent(event kernel.RetrievalEvent) Event {
	return Event{Type: EventRetrieval, Phase: PhaseRetrieving, Time: time.Now().UTC(), Retrieval: &event}
}
