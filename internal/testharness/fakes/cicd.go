package fakes

import (
	"context"
	"sync"

	"github.com/nickawilliams/bosun/internal/cicd"
)

// CICD is an in-memory cicd.CICD. Commands that dispatch workflows
// record their requests here; inspect Triggers() and Calls() after
// the run to assert on what was dispatched. Safe for concurrent use.
type CICD struct {
	mu sync.Mutex

	// triggers records every TriggerWorkflow request, in order.
	triggers []cicd.TriggerRequest

	// TriggerErr overrides default behavior to force the dispatch
	// error path. nil means success.
	TriggerErr error

	// NewErr makes the harness's CICD factory fail instead of handing
	// out this fake — see the NewErr knob on the other fakes.
	NewErr error

	// calls records the method names invoked, in order.
	calls []string
}

// NewCICD constructs an empty CICD.
func NewCICD() *CICD { return &CICD{} }

// Triggers returns a snapshot of dispatched workflow requests, in order.
func (c *CICD) Triggers() []cicd.TriggerRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]cicd.TriggerRequest, len(c.triggers))
	copy(out, c.triggers)
	return out
}

// Calls returns a snapshot of method calls in invocation order.
func (c *CICD) Calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.calls))
	copy(out, c.calls)
	return out
}

func (c *CICD) recordCall(name string) {
	c.calls = append(c.calls, name)
}

// --- cicd.CICD implementation ---

func (c *CICD) TriggerWorkflow(_ context.Context, req cicd.TriggerRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recordCall("TriggerWorkflow")
	if c.TriggerErr != nil {
		return c.TriggerErr
	}
	c.triggers = append(c.triggers, req)
	return nil
}

// Verify CICD satisfies cicd.CICD at compile time.
var _ cicd.CICD = (*CICD)(nil)
