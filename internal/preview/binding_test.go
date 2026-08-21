package preview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubStore struct {
	prop        json.RawMessage
	getErr      error
	setErr      error
	deleteErr   error
	setCalls    int
	deleteCalls int
	lastValue   any
}

func (s *stubStore) GetProperty(context.Context, string) (json.RawMessage, error) {
	return s.prop, s.getErr
}

func (s *stubStore) SetProperty(_ context.Context, _ string, value any) error {
	s.setCalls++
	s.lastValue = value
	return s.setErr
}

func (s *stubStore) DeleteProperty(context.Context, string) error {
	s.deleteCalls++
	return s.deleteErr
}

// TestBindingNilStoreIsAnEmptyRegistry pins the optional-tracker
// posture: a provider built without one still answers reads and
// silently drops writes, rather than panicking or erroring.
func TestBindingNilStoreIsAnEmptyRegistry(t *testing.T) {
	var b Binding

	if b.Available() {
		t.Error("Available() = true for a zero Binding")
	}
	if name := b.Name(t.Context(), "PROJ-1"); name != "" {
		t.Errorf("Name = %q, want empty", name)
	}
	if err := b.Bind(t.Context(), "PROJ-1", "brave-falcon"); err != nil {
		t.Errorf("Bind = %v, want nil", err)
	}
	if err := b.Unbind(t.Context(), "PROJ-1"); err != nil {
		t.Errorf("Unbind = %v, want nil", err)
	}
}

func TestBindingRoundTrip(t *testing.T) {
	store := &stubStore{}
	b := Binding{Store: store}

	if !b.Available() {
		t.Error("Available() = false with a store")
	}
	if err := b.Bind(t.Context(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// The stored shape is load-bearing: both adapters read it, so
	// switching preview.provider must not orphan a running env.
	raw, err := json.Marshal(store.lastValue)
	if err != nil {
		t.Fatalf("marshalling stored value: %v", err)
	}
	if got, want := string(raw), `{"preview_name":"brave-falcon"}`; got != want {
		t.Errorf("stored %s, want %s", got, want)
	}

	store.prop = raw
	if name := b.Name(t.Context(), "PROJ-1"); name != "brave-falcon" {
		t.Errorf("Name = %q, want brave-falcon", name)
	}

	if err := b.Unbind(t.Context(), "PROJ-1"); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if store.deleteCalls != 1 {
		t.Errorf("DeleteProperty called %d times, want 1", store.deleteCalls)
	}
}

// TestBindingNameIsAdvisory pins every non-success read path onto the
// same answer. The binding is a convenience, not a source of failure:
// a caller that can't read it treats the issue as unbound.
func TestBindingNameIsAdvisory(t *testing.T) {
	cases := map[string]*stubStore{
		"store error":        {getErr: errors.New("boom")},
		"absent property":    {prop: nil},
		"malformed JSON":     {prop: json.RawMessage(`{`)},
		"unexpected shape":   {prop: json.RawMessage(`"brave-falcon"`)},
		"empty stored name":  {prop: json.RawMessage(`{"preview_name":""}`)},
		"unrelated property": {prop: json.RawMessage(`{"other":"value"}`)},
	}
	for name, store := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (Binding{Store: store}).Name(t.Context(), "PROJ-1"); got != "" {
				t.Errorf("Name = %q, want empty", got)
			}
		})
	}
}

func TestBindingWriteErrorsPropagate(t *testing.T) {
	// Writes, unlike reads, report failure. Whether that failure is
	// fatal is the adapter's call — both treat it as best-effort — but
	// the Binding must not swallow it.
	setErr := errors.New("tracker down")
	if err := (Binding{Store: &stubStore{setErr: setErr}}).Bind(t.Context(), "PROJ-1", "x"); !errors.Is(err, setErr) {
		t.Errorf("Bind = %v, want the store's error", err)
	}
	deleteErr := errors.New("tracker down")
	if err := (Binding{Store: &stubStore{deleteErr: deleteErr}}).Unbind(t.Context(), "PROJ-1"); !errors.Is(err, deleteErr) {
		t.Errorf("Unbind = %v, want the store's error", err)
	}
}
