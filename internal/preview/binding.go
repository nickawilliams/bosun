package preview

import (
	"context"
	"encoding/json"
)

// PropertyStore is the slice of an issue tracker a preview adapter uses
// as its binding registry. issue.Tracker satisfies it; declaring the
// narrow shape here keeps the read/write of the binding property in one
// place without every adapter depending on the full tracker surface.
type PropertyStore interface {
	GetProperty(ctx context.Context, issueKey string) (json.RawMessage, error)
	SetProperty(ctx context.Context, issueKey string, value any) error
	DeleteProperty(ctx context.Context, issueKey string) error
}

// bindingEntry is the JSON shape stored at the issue's property:
// {"preview_name": "brave-falcon"}.
//
// Every adapter reads and writes this same shape deliberately. The
// binding is the user's env, not the provider's: switching
// preview.provider must not orphan a running environment, and it would
// if each adapter kept its own key.
type bindingEntry struct {
	PreviewName string `json:"preview_name"`
}

// Binding reads and writes the env-to-issue binding on an issue
// property. A zero Binding (nil Store) is usable and behaves as an
// empty registry: Name returns "", and Bind/Unbind are no-ops. That
// mirrors how the tracker is optional throughout — a preview provider
// without one still supports the read paths.
type Binding struct {
	Store PropertyStore
}

// Available reports whether a store is configured. Adapters use it to
// decide whether a binding write is worth attempting.
func (b Binding) Available() bool { return b.Store != nil }

// Name returns the env name bound to issueKey, or "" for any
// non-success path: no store, missing property, unexpected JSON shape,
// or store error. The binding is advisory — a caller that can't read it
// treats the issue as unbound rather than failing.
func (b Binding) Name(ctx context.Context, issueKey string) string {
	if b.Store == nil {
		return ""
	}
	raw, err := b.Store.GetProperty(ctx, issueKey)
	if err != nil || raw == nil {
		return ""
	}
	var entry bindingEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return ""
	}
	return entry.PreviewName
}

// Bind records name as the env bound to issueKey.
func (b Binding) Bind(ctx context.Context, issueKey, name string) error {
	if b.Store == nil {
		return nil
	}
	return b.Store.SetProperty(ctx, issueKey, bindingEntry{PreviewName: name})
}

// Unbind clears the binding on issueKey.
func (b Binding) Unbind(ctx context.Context, issueKey string) error {
	if b.Store == nil {
		return nil
	}
	return b.Store.DeleteProperty(ctx, issueKey)
}
