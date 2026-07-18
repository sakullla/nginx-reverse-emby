package generation

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReleasedGenerationsDropResourcesAndStayBounded(t *testing.T) {
	controller := NewDrainController(nil)
	resources := make([]*retentionResource, maxRetainedTerminalGenerations+8)
	for index := range resources {
		resources[index] = &retentionResource{}
		if err := controller.Activate(t.Context(), Generation{
			ID: fmt.Sprintf("generation-%02d", index+1), Revision: int64(index + 1), Resource: resources[index],
		}, nil, time.Minute); err != nil {
			t.Fatalf("Activate(%d) error = %v", index+1, err)
		}
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if len(controller.entries) > maxRetainedTerminalGenerations+1 {
		t.Fatalf("retained generation entries = %d, want <= %d", len(controller.entries), maxRetainedTerminalGenerations+1)
	}
	if len(controller.order) != len(controller.entries) {
		t.Fatalf("generation order/entry sizes = %d/%d", len(controller.order), len(controller.entries))
	}
	for id, entry := range controller.entries {
		if entry.released && entry.generation.Resource != nil {
			t.Fatalf("released generation %s retained its resource", id)
		}
	}
	for index := 0; index < len(resources)-1; index++ {
		if resources[index].destroyed != 1 {
			t.Fatalf("resource %d destroy count = %d, want 1", index+1, resources[index].destroyed)
		}
	}
}

type retentionResource struct {
	destroyed int
}

func (r *retentionResource) Destroy(context.Context) error {
	r.destroyed++
	return nil
}
