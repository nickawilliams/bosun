package cicd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
)

func TestClassifyProbeStatus(t *testing.T) {
	cases := []struct {
		status         int
		wantAlive      bool
		wantDefinitive bool
	}{
		{http.StatusOK, true, true},
		{http.StatusNoContent, true, true},
		{http.StatusMovedPermanently, true, true},
		{http.StatusUnauthorized, true, true}, // auth-gated env, host responds
		{http.StatusForbidden, true, true},    // ditto
		{http.StatusNotFound, false, true},    // env doesn't exist
		{http.StatusBadGateway, false, false}, // ambiguous, retry
		{http.StatusServiceUnavailable, false, false},
		{http.StatusGatewayTimeout, false, false},
		{0, false, false},
	}
	for _, tc := range cases {
		alive, definitive := classifyProbeStatus(tc.status)
		if alive != tc.wantAlive || definitive != tc.wantDefinitive {
			t.Errorf("classifyProbeStatus(%d) = (%v, %v), want (%v, %v)",
				tc.status, alive, definitive, tc.wantAlive, tc.wantDefinitive)
		}
	}
}

// --- Test doubles ---

// fakeTracker is a minimal issue.Tracker. Only the property methods carry
// state; everything else returns zero values. Methods record call counts
// so tests can verify side effects.
type fakeTracker struct {
	prop        json.RawMessage
	getErr      error
	setErr      error
	deleteErr   error
	getCalls    int
	setCalls    int
	deleteCalls int
	lastSet     any
}

func newFakeTracker() *fakeTracker { return &fakeTracker{} }

// withName seeds the tracker with a stored preview_name property.
func (f *fakeTracker) withName(name string) *fakeTracker {
	b, _ := json.Marshal(registryEntry{PreviewName: name})
	f.prop = b
	return f
}

func (f *fakeTracker) GetProperty(_ context.Context, _ string) (json.RawMessage, error) {
	f.getCalls++
	return f.prop, f.getErr
}

func (f *fakeTracker) SetProperty(_ context.Context, _ string, value any) error {
	f.setCalls++
	f.lastSet = value
	if f.setErr == nil {
		if b, err := json.Marshal(value); err == nil {
			f.prop = b
		}
	}
	return f.setErr
}

func (f *fakeTracker) DeleteProperty(_ context.Context, _ string) error {
	f.deleteCalls++
	if f.deleteErr == nil {
		f.prop = nil
	}
	return f.deleteErr
}

// Remaining issue.Tracker methods — unused by the adapter, stubbed.
func (f *fakeTracker) CreateIssue(context.Context, issue.CreateRequest) (issue.Issue, error) {
	return issue.Issue{}, nil
}
func (f *fakeTracker) GetIssue(context.Context, string) (issue.Issue, error) {
	return issue.Issue{}, nil
}
func (f *fakeTracker) SetStatus(context.Context, string, string) error { return nil }
func (f *fakeTracker) ListIssues(context.Context, issue.ListQuery) ([]issue.Issue, error) {
	return nil, nil
}
func (f *fakeTracker) BoardColumns(context.Context, string) ([]issue.BoardColumn, error) {
	return nil, nil
}
func (f *fakeTracker) ListBoards(context.Context, string) ([]issue.Board, error) {
	return nil, nil
}

// fakePipeline records workflow dispatches and returns a configured error.
type fakePipeline struct {
	err      error
	triggers []cicd.TriggerRequest
}

func newFakePipeline() *fakePipeline { return &fakePipeline{} }

func (f *fakePipeline) TriggerWorkflow(_ context.Context, req cicd.TriggerRequest) error {
	f.triggers = append(f.triggers, req)
	return f.err
}

// --- Helper ---

type providerBuilder struct {
	tracker     issue.Tracker
	pipeline    cicd.CICD
	urlPattern  string
	targets     []Target
	inputName   func(subStage, concept string) string
	targetsFunc func(context.Context, string) ([]Target, error)
}

func newBuilder() *providerBuilder {
	return &providerBuilder{
		inputName: func(_, concept string) string { return concept },
	}
}

func (b *providerBuilder) build(t *testing.T) preview.Provider {
	t.Helper()
	var tmpl *template.Template
	if b.urlPattern != "" {
		var err error
		tmpl, err = template.New("test").Parse(b.urlPattern)
		if err != nil {
			t.Fatalf("template parse: %v", err)
		}
	}
	targetsFunc := b.targetsFunc
	if targetsFunc == nil {
		targets := b.targets
		targetsFunc = func(context.Context, string) ([]Target, error) { return targets, nil }
	}
	opts := Options{
		Pipeline:    b.pipeline,
		Tracker:     b.tracker,
		Stage:       "preview",
		URLTemplate: tmpl,
		Targets:     targetsFunc,
		InputName:   b.inputName,
	}
	return New(opts)
}

// --- Get ---

func TestGet_NilTracker(t *testing.T) {
	p := newBuilder().build(t)
	_, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
}

func TestGet_EmptyProperty(t *testing.T) {
	p := newBuilder().
		withTracker(newFakeTracker()).
		build(t)
	_, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
}

func TestGet_MalformedProperty(t *testing.T) {
	tracker := newFakeTracker()
	tracker.prop = json.RawMessage("not valid json {")
	p := newBuilder().withTracker(tracker).build(t)
	_, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
}

func TestGet_NoURLTemplate(t *testing.T) {
	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().withTracker(tracker).build(t)
	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if env.Name != "brave-falcon" {
		t.Errorf("Name = %q, want brave-falcon", env.Name)
	}
	if env.URL != "" {
		t.Errorf("URL = %q, want empty (no template)", env.URL)
	}
	if env.Probed {
		t.Error("Probed = true, want false (no template ⇒ no probe)")
	}
}

func TestGet_Alive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().
		withTracker(tracker).
		withURLPattern(server.URL + "/{{.Name}}").
		build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !env.Probed || !env.Alive {
		t.Errorf("Probed=%v, Alive=%v, want both true", env.Probed, env.Alive)
	}
	if env.Name != "brave-falcon" || env.IssueKey != "PROJ-1" {
		t.Errorf("env = %+v", env)
	}
}

func TestGet_DeadKeepsBinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().
		withTracker(tracker).
		withURLPattern(server.URL + "/{{.Name}}").
		build(t)

	// Get is a pure read: a definitive-dead probe reports the binding as
	// probed-but-not-alive without deleting it (the env may be torn down
	// or a just-triggered deploy may still be in flight — a read must not
	// clobber the binding).
	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if env.Name != "brave-falcon" || env.IssueKey != "PROJ-1" {
		t.Errorf("env = %+v", env)
	}
	if !env.Probed || env.Alive {
		t.Errorf("Probed=%v, Alive=%v, want Probed=true, Alive=false", env.Probed, env.Alive)
	}
	if tracker.deleteCalls != 0 {
		t.Errorf("DeleteProperty called %d times, want 0 (read must not mutate)", tracker.deleteCalls)
	}
	if tracker.prop == nil {
		t.Error("binding was cleared; want it preserved")
	}
}

func TestGet_Indeterminate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().
		withTracker(tracker).
		withURLPattern(server.URL + "/{{.Name}}").
		build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	var pe *preview.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want ProbeError", err)
	}
	if env.Name != "brave-falcon" {
		t.Errorf("Name = %q, want brave-falcon (data preserved on indeterminate)", env.Name)
	}
	if env.URL == "" {
		t.Error("URL should be populated even on probe error")
	}
	if env.Probed || env.Alive {
		t.Errorf("Probed=%v Alive=%v, want both false on indeterminate", env.Probed, env.Alive)
	}
	if tracker.deleteCalls != 0 {
		t.Error("indeterminate probe should not trigger cleanup")
	}
}

// --- Inspect ---

func TestInspect_EmptyName(t *testing.T) {
	p := newBuilder().build(t)
	_, err := p.Inspect(context.Background(), "")
	if !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
}

func TestInspect_NoURLTemplate(t *testing.T) {
	p := newBuilder().build(t)
	env, err := p.Inspect(context.Background(), "brave-falcon")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if env.Name != "brave-falcon" || env.IssueKey != "" {
		t.Errorf("env = %+v, want Name=brave-falcon, IssueKey=''", env)
	}
	if env.Probed {
		t.Error("Probed = true, want false")
	}
}

func TestInspect_Alive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newBuilder().withURLPattern(server.URL + "/{{.Name}}").build(t)
	env, err := p.Inspect(context.Background(), "brave-falcon")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !env.Probed || !env.Alive {
		t.Errorf("Probed=%v Alive=%v, want both true", env.Probed, env.Alive)
	}
	if env.IssueKey != "" {
		t.Errorf("IssueKey = %q, want empty (Inspect is not issue-scoped)", env.IssueKey)
	}
}

func TestInspect_Indeterminate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	p := newBuilder().withURLPattern(server.URL + "/{{.Name}}").build(t)
	env, err := p.Inspect(context.Background(), "brave-falcon")

	var pe *preview.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v (%T), want *preview.ProbeError", err, err)
	}
	if env.Name != "brave-falcon" {
		t.Errorf("Name = %q, want brave-falcon (data preserved on indeterminate)", env.Name)
	}
	if env.URL == "" {
		t.Error("URL should be populated even on probe error")
	}
}

func TestInspect_DeadDoesNotAutoClear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Inspect doesn't take an issueKey and doesn't touch the tracker.
	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().
		withTracker(tracker).
		withURLPattern(server.URL + "/{{.Name}}").
		build(t)

	env, err := p.Inspect(context.Background(), "brave-falcon")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !env.Probed || env.Alive {
		t.Errorf("Probed=%v Alive=%v, want Probed=true Alive=false", env.Probed, env.Alive)
	}
	if tracker.deleteCalls != 0 {
		t.Error("Inspect must not touch the tracker")
	}
}

// --- Create ---

func TestCreate_EmptyName(t *testing.T) {
	p := newBuilder().withPipeline(newFakePipeline()).build(t)
	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1"})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestCreate_NilPipeline(t *testing.T) {
	p := newBuilder().build(t)
	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "x"})
	if !errors.Is(err, ErrNoPipeline) {
		t.Fatalf("err = %v, want ErrNoPipeline", err)
	}
}

func TestCreate_NoTargets(t *testing.T) {
	p := newBuilder().withPipeline(newFakePipeline()).build(t)
	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "x"})
	if !errors.Is(err, ErrNoWorkflow) {
		t.Fatalf("err = %v, want ErrNoWorkflow", err)
	}
}

func TestCreate_DispatchesAllTargets(t *testing.T) {
	pipe := newFakePipeline()
	tracker := newFakeTracker()
	targets := []Target{
		{Owner: "o1", Repo: "r1", Workflow: "w1.yml", Label: "r1"},
		{Owner: "o2", Repo: "r2", Workflow: "w2.yml", Label: "r2"},
	}
	p := newBuilder().
		withPipeline(pipe).
		withTracker(tracker).
		withTargets(targets).
		build(t)

	_, err := p.Create(context.Background(), preview.Claim{
		IssueKey: "PROJ-1",
		Name:     "brave-falcon",
		Services: []string{"api", "web"},
		Overrides: map[string]string{
			"api": "pr-123",
			"web": "pr-124",
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(pipe.triggers) != 2 {
		t.Fatalf("triggered %d workflows, want 2", len(pipe.triggers))
	}
	for i, tr := range pipe.triggers {
		if tr.Owner != targets[i].Owner || tr.Workflow != targets[i].Workflow {
			t.Errorf("trigger %d = %+v, want %+v", i, tr, targets[i])
		}
		if tr.Inputs["name"] != "brave-falcon" {
			t.Errorf("trigger %d name input = %q", i, tr.Inputs["name"])
		}
		if tr.Inputs["issue"] != "PROJ-1" {
			t.Errorf("trigger %d issue input = %q", i, tr.Inputs["issue"])
		}
		if tr.Inputs["services"] != "api,web" && tr.Inputs["services"] != "web,api" {
			t.Errorf("trigger %d services input = %q, want joined api,web", i, tr.Inputs["services"])
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(tr.Inputs["image-overrides"]), &got); err != nil {
			t.Errorf("trigger %d image-overrides not valid JSON: %v", i, err)
		}
		if got["api"] != "pr-123" || got["web"] != "pr-124" {
			t.Errorf("trigger %d overrides = %v", i, got)
		}
	}
	if tracker.setCalls != 1 {
		t.Errorf("SetProperty called %d times, want 1", tracker.setCalls)
	}
}

func TestCreate_DispatchErrorPropagates(t *testing.T) {
	pipe := newFakePipeline()
	pipe.err = errors.New("dispatch failed")
	tracker := newFakeTracker()
	p := newBuilder().
		withPipeline(pipe).
		withTracker(tracker).
		withTargets([]Target{{Owner: "o", Repo: "r", Workflow: "w.yml"}}).
		build(t)

	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "x"})
	if err == nil {
		t.Fatal("expected dispatch error to propagate")
	}
	if tracker.setCalls != 0 {
		t.Error("tracker should not be written on dispatch failure")
	}
}

func TestCreate_NilTrackerSkipsWrite(t *testing.T) {
	pipe := newFakePipeline()
	p := newBuilder().
		withPipeline(pipe).
		withTargets([]Target{{Owner: "o", Repo: "r", Workflow: "w.yml"}}).
		build(t)

	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "x"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(pipe.triggers) != 1 {
		t.Errorf("expected 1 trigger, got %d", len(pipe.triggers))
	}
}

// --- Adopt ---

func TestAdopt_EmptyName(t *testing.T) {
	p := newBuilder().build(t)
	if err := p.Adopt(context.Background(), "PROJ-1", ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestAdopt_NilTracker(t *testing.T) {
	p := newBuilder().build(t)
	if err := p.Adopt(context.Background(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("nil tracker should be graceful, got: %v", err)
	}
}

func TestAdopt_WritesProperty(t *testing.T) {
	tracker := newFakeTracker()
	p := newBuilder().withTracker(tracker).build(t)
	if err := p.Adopt(context.Background(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if tracker.setCalls != 1 {
		t.Errorf("SetProperty called %d times, want 1", tracker.setCalls)
	}
	var got registryEntry
	_ = json.Unmarshal(tracker.prop, &got)
	if got.PreviewName != "brave-falcon" {
		t.Errorf("stored property = %+v, want PreviewName=brave-falcon", got)
	}
}

// --- Destroy ---

func TestDestroy_EmptyName(t *testing.T) {
	p := newBuilder().withPipeline(newFakePipeline()).build(t)
	if err := p.Destroy(context.Background(), "PROJ-1", ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestDestroy_NilPipeline(t *testing.T) {
	p := newBuilder().build(t)
	if err := p.Destroy(context.Background(), "PROJ-1", "x"); !errors.Is(err, ErrNoPipeline) {
		t.Fatalf("err = %v, want ErrNoPipeline", err)
	}
}

func TestDestroy_NoTargets(t *testing.T) {
	p := newBuilder().withPipeline(newFakePipeline()).build(t)
	if err := p.Destroy(context.Background(), "PROJ-1", "x"); !errors.Is(err, ErrNoWorkflow) {
		t.Fatalf("err = %v, want ErrNoWorkflow", err)
	}
}

func TestDestroy_RefusesWithoutNameInput(t *testing.T) {
	// Override InputName to return empty for "name", simulating a missing
	// preview.down.inputs.name configuration. Refusing here matches the
	// legacy safety check that prevented "tear down everything" workflow
	// invocations.
	pipe := newFakePipeline()
	b := newBuilder().
		withPipeline(pipe).
		withTargets([]Target{{Owner: "o", Repo: "r", Workflow: "w.yml"}})
	b.inputName = func(_, _ string) string { return "" }
	p := b.build(t)

	if err := p.Destroy(context.Background(), "PROJ-1", "x"); err == nil {
		t.Fatal("expected refusal when name input is unconfigured")
	}
	if len(pipe.triggers) != 0 {
		t.Error("must not dispatch when name input is missing")
	}
}

func TestDestroy_DispatchesAndClears(t *testing.T) {
	pipe := newFakePipeline()
	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().
		withPipeline(pipe).
		withTracker(tracker).
		withTargets([]Target{{Owner: "o", Repo: "r", Workflow: "w.yml"}}).
		build(t)

	if err := p.Destroy(context.Background(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(pipe.triggers) != 1 {
		t.Errorf("triggers = %d, want 1", len(pipe.triggers))
	}
	if pipe.triggers[0].Inputs["name"] != "brave-falcon" {
		t.Errorf("dispatch name input = %q", pipe.triggers[0].Inputs["name"])
	}
	if pipe.triggers[0].Inputs["issue"] != "PROJ-1" {
		t.Errorf("dispatch issue input = %q", pipe.triggers[0].Inputs["issue"])
	}
	if tracker.deleteCalls != 1 {
		t.Errorf("DeleteProperty called %d times, want 1", tracker.deleteCalls)
	}
}

func TestDestroy_DispatchErrorSkipsClear(t *testing.T) {
	pipe := newFakePipeline()
	pipe.err = errors.New("dispatch failed")
	tracker := newFakeTracker().withName("brave-falcon")
	p := newBuilder().
		withPipeline(pipe).
		withTracker(tracker).
		withTargets([]Target{{Owner: "o", Repo: "r", Workflow: "w.yml"}}).
		build(t)

	if err := p.Destroy(context.Background(), "PROJ-1", "brave-falcon"); err == nil {
		t.Fatal("expected dispatch error to propagate")
	}
	if tracker.deleteCalls != 0 {
		t.Error("tracker must not be cleared on dispatch failure")
	}
}

// --- Builder fluent setters ---

func (b *providerBuilder) withTracker(t issue.Tracker) *providerBuilder {
	b.tracker = t
	return b
}
func (b *providerBuilder) withPipeline(p cicd.CICD) *providerBuilder {
	b.pipeline = p
	return b
}
func (b *providerBuilder) withURLPattern(s string) *providerBuilder {
	b.urlPattern = s
	return b
}
func (b *providerBuilder) withTargets(ts []Target) *providerBuilder {
	b.targets = ts
	return b
}
