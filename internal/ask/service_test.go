package ask_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/ai"
	appcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/app"
	askcore "github.com/BlueSkyXN/Feishu-SuperSearch/internal/ask"
	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/research"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

type fakeAnswerer struct {
	err error
}

func (fakeAnswerer) Name() string { return "fake" }
func (a fakeAnswerer) Answer(_ context.Context, request ai.AnswerRequest) (ai.Answer, error) {
	if a.err != nil {
		return ai.Answer{}, a.err
	}
	if len(request.EvidencePack.Evidence) == 0 {
		return ai.Answer{Text: "无证据", Partial: true}, nil
	}
	return ai.Answer{Text: "基于证据的回答"}, nil
}

func testApp(t *testing.T) *appcore.App {
	t.Helper()
	cfg := appcore.DefaultConfig()
	cfg.Backend = "mock"
	cfg.Storage = appcore.StorageConfig{Type: "memory", TTL: "45m"}
	app, err := appcore.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestAskRequiresAnswerer(t *testing.T) {
	app := testApp(t)
	_, err := (askcore.Service{Research: research.Service{Kernel: app.Engine, Planner: app.Planner}}).Run(context.Background(), askcore.Request{UserRequest: planner.UserRequest{Query: "A 项目延期"}})
	if err == nil || kernel.DetailFromError(err).Type != kernel.ErrUnsupported {
		t.Fatalf("error=%v", err)
	}
}

func TestAskFallsBackToDeterministicSummary(t *testing.T) {
	app := testApp(t)
	result, err := (askcore.Service{Research: research.Service{Kernel: app.Engine, Planner: app.Planner}, Answerer: fakeAnswerer{err: errors.New("model down")}}).Run(context.Background(), askcore.Request{UserRequest: planner.UserRequest{Query: "A 项目延期", Sources: []kernel.SourceID{kernel.SourceDocs, kernel.SourceMessages}, FetchTopK: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || result.Answer.Text == "" || len(result.Warnings) == 0 || len(result.Research.EvidencePack.Evidence) == 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestAskStreamsAnsweringAfterResearch(t *testing.T) {
	app := testApp(t)
	events := []research.Event{}
	_, err := (askcore.Service{Research: research.Service{Kernel: app.Engine, Planner: app.Planner}, Answerer: fakeAnswerer{}}).RunWithObserver(
		context.Background(),
		askcore.Request{UserRequest: planner.UserRequest{Query: "A 项目延期", Sources: []kernel.SourceID{kernel.SourceDocs}, FetchTopK: 1}},
		func(event research.Event) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	answering := -1
	researchComplete := -1
	complete := -1
	for index, event := range events {
		switch event.Phase {
		case research.PhaseResearchComplete:
			researchComplete = index
		case research.PhaseAnswering:
			answering = index
		case research.PhaseComplete:
			complete = index
		}
	}
	if researchComplete < 0 || answering <= researchComplete || complete <= answering {
		t.Fatalf("unexpected event order: %+v", events)
	}
}
