package larkcli

import (
	"strings"
	"testing"

	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
)

func TestTaskSearchUsesCurrentBooleanAndPaginationFlags(t *testing.T) {
	completed := false
	args, err := tasksSearchArgs(kernel.ProviderSearchRequest{
		Query:    "launch",
		PageSize: 25,
		Cursor:   "next-page",
		Filters:  kernel.SearchFilters{Completed: &completed, PersonIDs: []string{"ou_a", "ou_b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--page-token next-page", "--completed=false", "--assignee ou_a,ou_b"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "--page-size") {
		t.Fatalf("task +search does not support --page-size: %q", joined)
	}
}

func TestMessageSearchPreservesAllChatAndSenderFilters(t *testing.T) {
	args, err := messagesSearchArgs(kernel.ProviderSearchRequest{
		Query:   "launch",
		Filters: kernel.SearchFilters{ChatIDs: []string{"oc_a", "oc_b"}, SenderIDs: []string{"ou_a", "ou_b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--chat-id oc_a,oc_b", "--sender ou_a,ou_b"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
}

func TestEveryLarkCLIOperationHasCapabilityProbe(t *testing.T) {
	for _, spec := range BuiltinSpecs() {
		for operation, enabled := range spec.Descriptor.Operations {
			if !enabled {
				continue
			}
			_, explicit := spec.OperationProbes[operation]
			usesPrimary := (operation == kernel.OpSearch && spec.SearchArgs != nil) ||
				(operation == kernel.OpQuery && spec.QueryArgs != nil) ||
				(operation == kernel.OpResolve && spec.Descriptor.Source == kernel.SourcePeople)
			if !explicit && !usesPrimary {
				t.Fatalf("provider %s operation %s has no capability probe", spec.Descriptor.ID, operation)
			}
		}
	}
}
