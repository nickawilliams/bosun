package cli

import (
	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/services"
)

// Services bundles factory functions for the capability interfaces
// commands depend on. The active set is held in a package var so
// tests can swap individual factories for fakes via SetServices,
// without touching the wrapper functions every command call site
// uses (newIssueTracker, newCodeHost, etc.).
type Services struct {
	IssueTracker    func() (issue.Tracker, error)
	CodeHost        func() (code.Host, error)
	CICD            func() (cicd.CICD, error)
	Notifier        func() (notify.Notifier, error)
	PreviewProvider func(workspace string) (preview.Provider, error)
}

// factories is the active factory set. Replaced by SetServices in tests;
// the wrappers below dispatch through whatever is current.
//
// Initialized in init() rather than at var declaration to break a Go
// reachability cycle. defaultServices() stores function *values* (not
// invocations), but the compiler's init-order analysis traces through
// function bodies regardless of whether they're called. The path
//
//	factories -> defaultServices -> newPreviewProviderImpl ->
//	    newIssueTracker -> factories
//
// trips the cycle detector at var-init time. init() defers the
// assignment past the initializer phase, breaking the analysis cycle
// without any runtime cost. Confirmed with a minimal repro: removing
// init() and using `var factories = defaultServices()` reproduces the
// "initialization cycle" compile error.
var factories *Services

func init() { factories = defaultServices() }

// defaultServices returns the production factory set. Every capability
// but preview comes straight from the services registry, which owns
// provider construction; preview is composed here because its targets
// are resolved from the active workspace (see newPreviewProviderImpl).
func defaultServices() *Services {
	return &Services{
		IssueTracker:    func() (issue.Tracker, error) { return services.IssueTracker(providerConfig{}) },
		CodeHost:        func() (code.Host, error) { return services.CodeHost(providerConfig{}) },
		CICD:            func() (cicd.CICD, error) { return services.CICD(providerConfig{}) },
		Notifier:        func() (notify.Notifier, error) { return services.Notifier(providerConfig{}) },
		PreviewProvider: newPreviewProviderImpl,
	}
}

// GetServices returns the active factory set. Tests snapshot the
// current set before installing fakes so they can restore in t.Cleanup.
func GetServices() *Services { return factories }

// DefaultServices returns a fresh production factory set. Tests reach
// for it when one capability should run against its real adapter
// while the rest stay faked — `bosun preview` exercises the actual
// preview provider over a fake pipeline and tracker that way. Unlike
// ResetServices this installs nothing; the caller picks the fields it
// wants.
func DefaultServices() *Services { return defaultServices() }

// SetServices replaces the active factory set wholesale. Tests pass
// a Services with at least the fields their command uses populated;
// nil fields will panic if called by command code, which is the
// desired behavior — surfaces missing fakes immediately.
func SetServices(s *Services) { factories = s }

// ResetServices restores the default (real-adapter) factory set.
// Tests register this via t.Cleanup so state doesn't leak between tests.
func ResetServices() { factories = defaultServices() }

// The wrappers below are the only call sites command code uses;
// keeping them as functions (rather than calling factories.IssueTracker
// directly everywhere) means the swap point is a single dereference
// and the existing call sites — newIssueTracker(), newCodeHost(), etc. —
// don't change.

func newIssueTracker() (issue.Tracker, error) { return factories.IssueTracker() }
func newCodeHost() (code.Host, error)         { return factories.CodeHost() }
func newCICD() (cicd.CICD, error)             { return factories.CICD() }
func newNotifier() (notify.Notifier, error)   { return factories.Notifier() }
func newPreviewProvider(workspace string) (preview.Provider, error) {
	return factories.PreviewProvider(workspace)
}
