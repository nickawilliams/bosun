package ephemeral

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"text/template"

	"github.com/nickawilliams/bosun/internal/preview"
)

// --- Harness ---

// fakeStore is an in-memory preview.PropertyStore.
type fakeStore struct {
	mu          sync.Mutex
	prop        json.RawMessage
	getErr      error
	setErr      error
	setCalls    int
	deleteCalls int
}

func (f *fakeStore) GetProperty(_ context.Context, _ string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prop, f.getErr
}

func (f *fakeStore) SetProperty(_ context.Context, _ string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.prop, _ = json.Marshal(value)
	return nil
}

func (f *fakeStore) DeleteProperty(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.prop = nil
	return nil
}

// bound returns a store already holding a binding to name.
func bound(name string) *fakeStore {
	raw, _ := json.Marshal(map[string]string{"preview_name": name})
	return &fakeStore{prop: raw}
}

// storedName reads the name the store currently holds.
func storedName(t *testing.T, s *fakeStore) string {
	t.Helper()
	if s.prop == nil {
		return ""
	}
	var entry struct {
		PreviewName string `json:"preview_name"`
	}
	if err := json.Unmarshal(s.prop, &entry); err != nil {
		t.Fatalf("stored property isn't the binding shape: %v", err)
	}
	return entry.PreviewName
}

// recorder captures the requests a test's handler received.
type recorder struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
}

func (r *recorder) record(req *http.Request, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, req.URL.Path)
	r.bodies = append(r.bodies, body)
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

func (r *recorder) lastBody() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return ""
	}
	return r.bodies[len(r.bodies)-1]
}

// builder assembles a provider over a test server.
type builder struct {
	handler  http.HandlerFunc
	store    preview.PropertyStore
	tmpl     *template.Template
	token    func(context.Context) (string, error)
	noServer bool
	rec      *recorder
}

func newBuilder() *builder {
	return &builder{rec: &recorder{}}
}

func (b *builder) handle(h http.HandlerFunc) *builder { b.handler = h; return b }
func (b *builder) store_(s preview.PropertyStore) *builder {
	b.store = s
	return b
}
func (b *builder) withTemplate() *builder {
	b.tmpl = template.Must(template.New("u").Parse("https://{{.Name}}.example.dev"))
	return b
}
func (b *builder) withToken(f func(context.Context) (string, error)) *builder {
	b.token = f
	return b
}
func (b *builder) withoutServer() *builder { b.noServer = true; return b }

// build returns the provider plus the request recorder.
func (b *builder) build(t *testing.T) (preview.Provider, *recorder) {
	t.Helper()
	baseURL := ""
	if !b.noServer {
		handler := b.handler
		if handler == nil {
			handler = func(w http.ResponseWriter, _ *http.Request) { writeDeployments(w) }
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := readAll(r)
			b.rec.record(r, body)
			handler(w, r)
		}))
		t.Cleanup(srv.Close)
		baseURL = srv.URL
	}
	token := b.token
	if token == nil {
		token = func(context.Context) (string, error) { return "test-token", nil }
	}
	return New(Options{
		BaseURL:     baseURL,
		Token:       token,
		Tracker:     b.store,
		URLTemplate: b.tmpl,
	}), b.rec
}

func readAll(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	raw, _ := io.ReadAll(r.Body)
	return string(raw)
}

// writeDeployments writes a deployments envelope holding entries.
func writeDeployments(w http.ResponseWriter, entries ...string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"deployments":[%s]}`, strings.Join(entries, ","))
}

// entry renders one deployment JSON object.
func entry(name, status string, extra ...string) string {
	fields := []string{
		fmt.Sprintf(`"name":%q`, name),
		fmt.Sprintf(`"status":%q`, status),
		`"deployedBy":"someone"`,
		fmt.Sprintf(`"url":"https://api-said-%s.example.dev"`, name),
	}
	return "{" + strings.Join(append(fields, extra...), ",") + "}"
}

// --- Get ---

func TestGet_NoBindingIsEmptyState(t *testing.T) {
	p, rec := newBuilder().store_(&fakeStore{}).build(t)

	_, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
	if rec.calls() != 0 {
		t.Errorf("made %d API calls for an unbound issue, want 0", rec.calls())
	}
}

func TestGet_NilStoreReportsNoEnvironment(t *testing.T) {
	p, _ := newBuilder().build(t)

	if _, err := p.Get(context.Background(), "PROJ-1"); !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
}

func TestGet_ActiveEnvironment(t *testing.T) {
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		withTemplate().
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("brave-falcon", "active"))
		}).build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !env.Probed || env.Status != preview.StatusActive {
		t.Errorf("Probed=%v Status=%v, want true, active", env.Probed, env.Status)
	}
	if !env.Alive() {
		t.Error("Alive() = false, want true")
	}
	if env.IssueKey != "PROJ-1" {
		t.Errorf("IssueKey = %q, want PROJ-1", env.IssueKey)
	}
	if env.DeployedBy != "someone" {
		t.Errorf("DeployedBy = %q, want someone", env.DeployedBy)
	}
	// The configured template wins over the URL the API reports so a URL
	// renders identically whether the env exists yet or not.
	if want := "https://brave-falcon.example.dev"; env.URL != want {
		t.Errorf("URL = %q, want %q", env.URL, want)
	}
}

func TestGet_DegradedNamesFailedServices(t *testing.T) {
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("brave-falcon", "degraded", `"failedServices":["api","worker"]`))
		}).build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if env.Status != preview.StatusDegraded {
		t.Fatalf("Status = %v, want degraded", env.Status)
	}
	if !env.Alive() {
		t.Error("Alive() = false for a degraded env, want true — it is serving")
	}
	if got := strings.Join(env.FailedServices, ","); got != "api,worker" {
		t.Errorf("FailedServices = %v, want [api worker]", env.FailedServices)
	}
}

// TestGet_TranslatesEveryStatus is the point of the adapter: the probe
// the cicd adapter uses collapses all of these into alive-or-not.
func TestGet_TranslatesEveryStatus(t *testing.T) {
	cases := []struct {
		api        string
		want       preview.Status
		wantProbed bool
	}{
		{"creating", preview.StatusCreating, true},
		{"active", preview.StatusActive, true},
		{"degraded", preview.StatusDegraded, true},
		{"deleting", preview.StatusDeleting, true},
		{"cleaned_up", preview.StatusGone, true},
		// A taxonomy this build predates: reported as unverified so
		// callers redeploy under the stored name rather than treating a
		// live env as torn down.
		{"teleporting", preview.StatusUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.api, func(t *testing.T) {
			p, _ := newBuilder().
				store_(bound("brave-falcon")).
				handle(func(w http.ResponseWriter, _ *http.Request) {
					writeDeployments(w, entry("brave-falcon", tc.api))
				}).build(t)

			env, err := p.Get(context.Background(), "PROJ-1")
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if env.Status != tc.want {
				t.Errorf("Status = %v, want %v", env.Status, tc.want)
			}
			if env.Probed != tc.wantProbed {
				t.Errorf("Probed = %v, want %v", env.Probed, tc.wantProbed)
			}
		})
	}
}

func TestGet_AbsentEnvKeepsBinding(t *testing.T) {
	store := bound("brave-falcon")
	p, _ := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("someone-elses", "active"))
		}).build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !env.Probed || env.Status != preview.StatusGone {
		t.Errorf("Probed=%v Status=%v, want true, gone", env.Probed, env.Status)
	}
	if env.Name != "brave-falcon" {
		t.Errorf("Name = %q, want the bound name back", env.Name)
	}
	// Get is a pure read: a deploy dispatched moments ago isn't listed
	// yet, and dropping the binding here would orphan it.
	if storedName(t, store) != "brave-falcon" {
		t.Error("Get cleared the binding for an env the API didn't list")
	}
	if store.deleteCalls != 0 {
		t.Errorf("DeleteProperty called %d times during a read, want 0", store.deleteCalls)
	}
}

func TestGet_AuthFailureCarriesTheBinding(t *testing.T) {
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		withTemplate().
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
		}).build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	// Registry is the source of truth: the caller can still render what
	// is bound even though nothing could be verified.
	if env.Name != "brave-falcon" || env.URL == "" {
		t.Errorf("env = %+v, want the binding populated", env)
	}
	if env.Probed {
		t.Error("Probed = true after an auth failure, want false")
	}
	// An expired token is definitive. Retrying it just delays the
	// re-auth prompt.
	var pe *preview.ProbeError
	if errors.As(err, &pe) {
		t.Error("auth failure reported as an indeterminate probe")
	}
}

func TestGet_ServerErrorIsAnIndeterminateProbe(t *testing.T) {
	p, rec := newBuilder().
		store_(bound("brave-falcon")).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}).build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	var pe *preview.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *preview.ProbeError", err)
	}
	if !strings.HasSuffix(pe.URL, pathDeployments) {
		t.Errorf("ProbeError.URL = %q, want it to name the endpoint", pe.URL)
	}
	if env.Name != "brave-falcon" {
		t.Errorf("Name = %q, want the binding back alongside the error", env.Name)
	}
	if rec.calls() != 2 {
		t.Errorf("made %d attempts, want 2 (one retry)", rec.calls())
	}
}

func TestGet_RetrySucceedsAfterTransientFailure(t *testing.T) {
	var calls int
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeDeployments(w, entry("brave-falcon", "active"))
		}).build(t)

	env, err := p.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err after retry: %v", err)
	}
	if env.Status != preview.StatusActive {
		t.Errorf("Status = %v, want active", env.Status)
	}
}

func TestGet_UnreachableHostIsAnIndeterminateProbe(t *testing.T) {
	// A base URL with no listener behind it: the transport fails rather
	// than answering, which is the other half of "couldn't verify".
	p := New(Options{
		BaseURL: "http://127.0.0.1:1",
		Token:   func(context.Context) (string, error) { return "t", nil },
		Tracker: bound("brave-falcon"),
	})

	_, err := p.Get(context.Background(), "PROJ-1")
	var pe *preview.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *preview.ProbeError", err)
	}
}

func TestGet_MalformedResponseIsIndeterminate(t *testing.T) {
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not json`))
		}).build(t)

	_, err := p.Get(context.Background(), "PROJ-1")
	var pe *preview.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *preview.ProbeError", err)
	}
}

func TestGet_UnreadableBindingIsEmptyState(t *testing.T) {
	// A tracker that errors, and one holding something that isn't the
	// binding shape, both read as "nothing bound" — the binding is
	// advisory, so an unreadable one must not fail the command.
	for name, store := range map[string]*fakeStore{
		"tracker error": {getErr: errors.New("boom")},
		"wrong shape":   {prop: json.RawMessage(`["not","an","object"]`)},
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := newBuilder().store_(store).build(t)
			if _, err := p.Get(context.Background(), "PROJ-1"); !errors.Is(err, preview.ErrNoEnvironment) {
				t.Fatalf("err = %v, want ErrNoEnvironment", err)
			}
		})
	}
}

// --- Inspect ---

func TestInspect_EmptyName(t *testing.T) {
	p, rec := newBuilder().build(t)

	if _, err := p.Inspect(context.Background(), ""); !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment", err)
	}
	if rec.calls() != 0 {
		t.Errorf("made %d API calls for an empty name, want 0", rec.calls())
	}
}

func TestInspect_IsRegistryFree(t *testing.T) {
	store := bound("brave-falcon")
	p, _ := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("other-env", "active"))
		}).build(t)

	env, err := p.Inspect(context.Background(), "other-env")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if env.IssueKey != "" {
		t.Errorf("IssueKey = %q, want empty — Inspect doesn't consult the registry", env.IssueKey)
	}
	if env.Status != preview.StatusActive {
		t.Errorf("Status = %v, want active", env.Status)
	}
}

func TestInspect_FreeNameIsNotAnError(t *testing.T) {
	p, _ := newBuilder().
		handle(func(w http.ResponseWriter, _ *http.Request) { writeDeployments(w) }).
		build(t)

	env, err := p.Inspect(context.Background(), "brave-falcon")
	if err != nil {
		t.Fatalf("unexpected err for an unused name: %v", err)
	}
	if !env.Probed || env.Status != preview.StatusGone {
		t.Errorf("Probed=%v Status=%v, want true, gone", env.Probed, env.Status)
	}
	if env.Alive() {
		t.Error("Alive() = true for an unused name")
	}
}

func TestInspect_MatchesNameCaseInsensitively(t *testing.T) {
	// The backend lowercases names for its own bookkeeping, so a
	// differently-cased name is the same env, not a free one.
	p, _ := newBuilder().
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("Brave-Falcon", "active"))
		}).build(t)

	env, err := p.Inspect(context.Background(), "brave-falcon")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if env.Status != preview.StatusActive {
		t.Errorf("Status = %v, want active — a case difference isn't a different env", env.Status)
	}
	if env.Name != "brave-falcon" {
		t.Errorf("Name = %q, want the caller's spelling preserved", env.Name)
	}
}

func TestInspect_FallsBackToTheAPIURL(t *testing.T) {
	p, _ := newBuilder(). // no URL template configured
				handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("brave-falcon", "active"))
		}).build(t)

	env, err := p.Inspect(context.Background(), "brave-falcon")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if want := "https://api-said-brave-falcon.example.dev"; env.URL != want {
		t.Errorf("URL = %q, want the API's %q", env.URL, want)
	}
}

// --- Create ---

func TestCreate_DispatchesAndBinds(t *testing.T) {
	store := &fakeStore{}
	p, rec := newBuilder().
		store_(store).
		withTemplate().
		handle(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"runId":42}`))
		}).build(t)

	env, err := p.Create(context.Background(), preview.Claim{
		IssueKey:      "PROJ-1",
		Name:          "brave-falcon",
		DefaultBranch: "release-2",
		Overrides:     map[string]string{"api": "pr-7"},
		// Services is deliberately populated and deliberately unused:
		// absence from Overrides means "run the default branch", so a
		// subset would leave the env half-built.
		Services: []string{"api"},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	body := rec.lastBody()
	var got deployRequest
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("request body isn't the deploy shape: %v (%s)", err, body)
	}
	if got.EphemeralName != "brave-falcon" {
		t.Errorf("ephemeralName = %q, want brave-falcon", got.EphemeralName)
	}
	if got.DefaultBranch != "release-2" {
		t.Errorf("defaultBranch = %q, want release-2", got.DefaultBranch)
	}
	// The API forwards this straight into a workflow_dispatch input,
	// which only accepts strings — an object would arrive unparseable.
	if got.ImageOverrides != `{"api":"pr-7"}` {
		t.Errorf("imageOverrides = %q, want a JSON string", got.ImageOverrides)
	}
	if strings.Contains(body, `"services"`) {
		t.Errorf("request body carries a services field: %s", body)
	}

	if storedName(t, store) != "brave-falcon" {
		t.Errorf("stored binding = %q, want brave-falcon", storedName(t, store))
	}
	// Dispatched, not landed. Callers render the URL before the env is up.
	if env.Probed {
		t.Error("Probed = true straight out of Create, want false")
	}
	if env.URL != "https://brave-falcon.example.dev" {
		t.Errorf("URL = %q, want the templated URL", env.URL)
	}
	if env.IssueKey != "PROJ-1" {
		t.Errorf("IssueKey = %q, want PROJ-1", env.IssueKey)
	}
}

func TestCreate_OmitsEmptyOverrides(t *testing.T) {
	p, rec := newBuilder().
		store_(&fakeStore{}).
		handle(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }).
		build(t)

	if _, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "brave-falcon"}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// An absent imageOverrides is how the API is told "every service runs
	// the default branch"; sending "" explicitly would say the same
	// thing, but an empty map should not manufacture a key.
	if body := rec.lastBody(); strings.Contains(body, "imageOverrides") {
		t.Errorf("body carries imageOverrides for an empty map: %s", body)
	}
}

func TestCreate_RefusesEmptyName(t *testing.T) {
	p, rec := newBuilder().store_(&fakeStore{}).build(t)

	if _, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1"}); err == nil {
		t.Fatal("Create succeeded with an empty name")
	}
	if rec.calls() != 0 {
		t.Errorf("made %d API calls for an empty name, want 0", rec.calls())
	}
}

func TestCreate_SurfacesTheAPIsReason(t *testing.T) {
	p, _ := newBuilder().
		store_(&fakeStore{}).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Invalid ephemeral name","details":"Single words are not allowed."}`))
		}).build(t)

	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "falcon"})
	if err == nil {
		t.Fatal("Create succeeded on a 400")
	}
	if !strings.Contains(err.Error(), "Single words are not allowed") {
		t.Errorf("err = %v, want the API's own explanation", err)
	}
}

func TestCreate_AuthFailure(t *testing.T) {
	store := &fakeStore{}
	p, _ := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}).build(t)

	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "brave-falcon"})
	if !errors.Is(err, preview.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	// Nothing was provisioned, so nothing should be bound to it.
	if store.setCalls != 0 {
		t.Errorf("wrote a binding for a rejected deploy (%d calls)", store.setCalls)
	}
}

func TestCreate_BindingFailureDoesNotFailTheDeploy(t *testing.T) {
	store := &fakeStore{setErr: errors.New("tracker down")}
	p, _ := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }).
		build(t)

	// The workflow is already in flight by this point. Reporting the
	// binding write as a failure would invite a retry that deploys twice.
	if _, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "brave-falcon"}); err != nil {
		t.Fatalf("Create failed on a binding-write error: %v", err)
	}
}

func TestCreate_WithoutABaseURL(t *testing.T) {
	p, _ := newBuilder().withoutServer().store_(&fakeStore{}).build(t)

	_, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "brave-falcon"})
	if !errors.Is(err, preview.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// --- Adopt ---

func TestAdopt_BindsWithoutDeploying(t *testing.T) {
	store := &fakeStore{}
	p, rec := newBuilder().store_(store).build(t)

	if err := p.Adopt(context.Background(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if storedName(t, store) != "brave-falcon" {
		t.Errorf("stored binding = %q, want brave-falcon", storedName(t, store))
	}
	// The whole point of Adopt is claiming a running env without
	// touching it.
	if rec.calls() != 0 {
		t.Errorf("Adopt made %d API calls, want 0", rec.calls())
	}
}

func TestAdopt_RefusesEmptyName(t *testing.T) {
	p, _ := newBuilder().store_(&fakeStore{}).build(t)

	if err := p.Adopt(context.Background(), "PROJ-1", ""); err == nil {
		t.Fatal("Adopt succeeded with an empty name")
	}
}

// --- Destroy ---

func TestDestroy_DispatchesAndUnbinds(t *testing.T) {
	store := bound("brave-falcon")
	p, rec := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"success":true}`)) }).
		build(t)

	if err := p.Destroy(context.Background(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var got deleteRequest
	if err := json.Unmarshal([]byte(rec.lastBody()), &got); err != nil {
		t.Fatalf("request body isn't the delete shape: %v", err)
	}
	if got.EphemeralName != "brave-falcon" {
		t.Errorf("ephemeralName = %q, want brave-falcon", got.EphemeralName)
	}
	if store.deleteCalls != 1 {
		t.Errorf("DeleteProperty called %d times, want 1", store.deleteCalls)
	}
}

func TestDestroy_RefusesBlankName(t *testing.T) {
	// A teardown workflow with no name commonly reads as "clean
	// everything", so a whitespace-only name must not reach the API.
	for _, name := range []string{"", "   "} {
		p, rec := newBuilder().store_(&fakeStore{}).build(t)
		if err := p.Destroy(context.Background(), "PROJ-1", name); err == nil {
			t.Errorf("Destroy(%q) succeeded, want a refusal", name)
		}
		if rec.calls() != 0 {
			t.Errorf("Destroy(%q) made %d API calls, want 0", name, rec.calls())
		}
	}
}

func TestDestroy_IsIdempotent(t *testing.T) {
	store := bound("brave-falcon")
	p, _ := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}).build(t)

	// Tearing down an env that is already gone is success, and the
	// binding still needs clearing.
	if err := p.Destroy(context.Background(), "PROJ-1", "brave-falcon"); err != nil {
		t.Fatalf("Destroy on a missing env returned %v, want nil", err)
	}
	if store.deleteCalls != 1 {
		t.Errorf("DeleteProperty called %d times, want 1", store.deleteCalls)
	}
}

func TestDestroy_ReportsRealFailures(t *testing.T) {
	store := bound("brave-falcon")
	p, _ := newBuilder().
		store_(store).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}).build(t)

	if err := p.Destroy(context.Background(), "PROJ-1", "brave-falcon"); err == nil {
		t.Fatal("Destroy succeeded on a 500")
	}
	// The teardown never dispatched, so the binding must survive for the
	// retry to have something to tear down.
	if store.deleteCalls != 0 {
		t.Errorf("cleared the binding after a failed teardown (%d calls)", store.deleteCalls)
	}
}

// --- List ---

func TestList_ReturnsTheFleet(t *testing.T) {
	p, _ := newBuilder().
		withTemplate().
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w,
				entry("brave-falcon", "active"),
				entry("wobbly-turtle", "creating"),
				// A provision run whose name hasn't been recovered from
				// the job logs yet: nothing can address it, so it is not
				// listable.
				`{"name":null,"status":"creating","deployedBy":"x","url":null}`,
			)
		}).build(t)

	lister, ok := p.(preview.Lister)
	if !ok {
		t.Fatal("provider does not implement preview.Lister")
	}
	envs, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("List returned %d envs, want 2 (the unnamed run skipped)", len(envs))
	}
	if envs[0].Name != "brave-falcon" || envs[0].Status != preview.StatusActive {
		t.Errorf("envs[0] = %+v", envs[0])
	}
	if envs[1].Status != preview.StatusCreating {
		t.Errorf("envs[1].Status = %v, want creating", envs[1].Status)
	}
	// The registry is keyed by issue, so a name can't be walked back to
	// one; the caller correlates.
	if envs[0].IssueKey != "" {
		t.Errorf("IssueKey = %q, want empty", envs[0].IssueKey)
	}
}

func TestList_EmptyFleet(t *testing.T) {
	p, _ := newBuilder().handle(func(w http.ResponseWriter, _ *http.Request) { writeDeployments(w) }).build(t)

	envs, err := p.(preview.Lister).List(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(envs) != 0 {
		t.Errorf("List returned %d envs, want 0", len(envs))
	}
}

func TestList_PropagatesFailure(t *testing.T) {
	p, _ := newBuilder().
		handle(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }).
		build(t)

	if _, err := p.(preview.Lister).List(context.Background()); !errors.Is(err, preview.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

// --- Name grammar ---

func TestValidateName(t *testing.T) {
	p, _ := newBuilder().build(t)
	v, ok := p.(preview.NameValidator)
	if !ok {
		t.Fatal("provider does not implement preview.NameValidator")
	}

	cases := []struct {
		name string
		ok   bool
	}{
		{"brave-falcon", true},
		{"pink-flying-duck", true},
		// The one place this provider is stricter than the shared floor:
		// the API's namespace grammar demands two or more segments.
		{"falcon", false},
		{"", false},
		{"Brave-Falcon", false},
		{"brave_falcon", false},
		{"brave-", false},
	}
	for _, tc := range cases {
		err := v.ValidateName(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}

	// The single-word rejection must be routed through the provider —
	// the shared validator accepts it.
	if err := preview.ValidateName("falcon"); err != nil {
		t.Errorf("preview.ValidateName(%q) = %v, want nil", "falcon", err)
	}
	if err := preview.ProviderValidateName(p, "falcon"); err == nil {
		t.Error("ProviderValidateName accepted a single word; the provider's grammar was skipped")
	}
}

// --- Token handling ---

func TestToken_ResolvedOncePerProvider(t *testing.T) {
	var calls int
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		withToken(func(context.Context) (string, error) {
			calls++
			return "test-token", nil
		}).
		handle(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q, want a bearer token", got)
			}
			writeDeployments(w, entry("brave-falcon", "active"))
		}).build(t)

	for range 3 {
		if _, err := p.Get(context.Background(), "PROJ-1"); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	}
	// Each resolution shells out to the GitHub CLI, and the token does
	// not change mid-command.
	if calls != 1 {
		t.Errorf("token source called %d times, want 1", calls)
	}
}

func TestToken_EmptyIsAnAuthFailure(t *testing.T) {
	p, rec := newBuilder().
		store_(bound("brave-falcon")).
		withToken(func(context.Context) (string, error) { return "  ", nil }).
		build(t)

	_, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if rec.calls() != 0 {
		t.Errorf("made %d unauthenticated calls, want 0", rec.calls())
	}
}

func TestToken_SourceErrorPropagates(t *testing.T) {
	sentinel := errors.New("gh exploded")
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		withToken(func(context.Context) (string, error) { return "", sentinel }).
		build(t)

	if _, err := p.Get(context.Background(), "PROJ-1"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the token source's error", err)
	}
}

func TestGitHubCLIToken_FallsBackToTheEnvironment(t *testing.T) {
	// PATH emptied so the gh lookup can't succeed, isolating the
	// GITHUB_TOKEN arm.
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_TOKEN", "env-token")

	got, err := GitHubCLIToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "env-token" {
		t.Errorf("token = %q, want env-token", got)
	}
}

func TestGitHubCLIToken_NoCredential(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("GITHUB_TOKEN", "")

	if _, err := GitHubCLIToken(context.Background()); !errors.Is(err, preview.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

// --- Base URL handling ---

func TestBaseURL_TrailingSlashIsTolerated(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeDeployments(w)
	}))
	t.Cleanup(srv.Close)

	p := New(Options{
		BaseURL: srv.URL + "/",
		Token:   func(context.Context) (string, error) { return "t", nil },
	})
	if _, err := p.(preview.Lister).List(context.Background()); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if gotPath != pathDeployments {
		t.Errorf("requested %q, want %q — the slash was doubled", gotPath, pathDeployments)
	}
}

func TestBaseURL_UnsetIsNotConfigured(t *testing.T) {
	p, _ := newBuilder().withoutServer().store_(bound("brave-falcon")).build(t)

	// Not an indeterminate probe: there is nothing to talk to, and a
	// retry would fail identically. Callers treat it the way they treat
	// any missing optional dependency — skip the step and say why.
	_, err := p.Get(context.Background(), "PROJ-1")
	if !errors.Is(err, preview.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestBaseURL_MalformedIsReportedOnce(t *testing.T) {
	p := New(Options{
		BaseURL: "http://[::1]:namedport",
		Token:   func(context.Context) (string, error) { return "t", nil },
		Tracker: bound("brave-falcon"),
	})

	_, err := p.Get(context.Background(), "PROJ-1")
	if err == nil {
		t.Fatal("Get succeeded against a malformed base URL")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("err = %v, want it to name the offending config key", err)
	}
}
