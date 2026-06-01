package cli

import (
	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/preview"
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
	PreviewProvider func(workspace string, onInfo func(action, value string)) (preview.Provider, error)
}

// services is the active factory set. Replaced by SetServices in tests;
// the wrappers below dispatch through whatever is current.
//
// Initialized in init() rather than at var declaration to break a Go
// reachability cycle. defaultServices() stores function *values* (not
// invocations), but the compiler's init-order analysis traces through
// function bodies regardless of whether they're called. The path
//
//	services -> defaultServices -> newPreviewProviderWithInfoImpl ->
//	    newIssueTracker -> services
//
// trips the cycle detector at var-init time. init() defers the
// assignment past the initializer phase, breaking the analysis cycle
// without any runtime cost. Confirmed with a minimal repro: removing
// init() and using `var services = defaultServices()` reproduces the
// "initialization cycle for services" compile error.
var services *Services

func init() { services = defaultServices() }

// defaultServices returns the production factory set — each field
// points at the real adapter constructor (newIssueTrackerImpl etc.).
func defaultServices() *Services {
	return &Services{
		IssueTracker:    newIssueTrackerImpl,
		CodeHost:        newCodeHostImpl,
		CICD:            newCICDImpl,
		Notifier:        newNotifierImpl,
		PreviewProvider: newPreviewProviderWithInfoImpl,
	}
}

// GetServices returns the active factory set. Tests snapshot the
// current set before installing fakes so they can restore in t.Cleanup.
func GetServices() *Services { return services }

// SetServices replaces the active factory set wholesale. Tests pass
// a Services with at least the fields their command uses populated;
// nil fields will panic if called by command code, which is the
// desired behavior — surfaces missing fakes immediately.
func SetServices(s *Services) { services = s }

// ResetServices restores the default (real-adapter) factory set.
// Tests register this via t.Cleanup so state doesn't leak between tests.
func ResetServices() { services = defaultServices() }

// The wrappers below are the only call sites command code uses;
// keeping them as functions (rather than calling services.IssueTracker
// directly everywhere) means the swap point is a single dereference
// and the existing call sites — newIssueTracker(), newCodeHost(), etc. —
// don't change.
//
// The real implementations moved to *Impl functions in services.go.

func newIssueTracker() (issue.Tracker, error) { return services.IssueTracker() }
func newCodeHost() (code.Host, error)         { return services.CodeHost() }
func newCICD() (cicd.CICD, error)             { return services.CICD() }
func newNotifier() (notify.Notifier, error)   { return services.Notifier() }
func newPreviewProviderWithInfo(workspace string, onInfo func(action, value string)) (preview.Provider, error) {
	return services.PreviewProvider(workspace, onInfo)
}
