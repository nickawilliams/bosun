package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// TestSuppressPrompts pins the gate `bosun doctor` relies on. Without
// it a read-only diagnostic can stop to collect a value — and
// requireConfig's completion path PERSISTS what it collects, so the
// report would edit the project's config file as a side effect of
// being run. The prompt is transitive (a check reaches a provider
// factory, which requires a config group, which resolves a missing
// key), so the gate sits at isInteractive rather than at each check.
func TestSuppressPrompts(t *testing.T) {
	// A non-*os.File reader counts as interactive — the injection the
	// test harness relies on — so this is the state a test session is
	// already in, and the one the suppression has to override.
	t.Cleanup(ui.ResetStreams)
	ui.SetStreams(strings.NewReader(""), io.Discard, io.Discard)

	if !isInteractive() {
		t.Fatal("isInteractive() = false with an injected reader; nothing to suppress")
	}

	restore := suppressPrompts()
	if isInteractive() {
		t.Error("isInteractive() = true while prompts are suppressed")
	}

	// requireConfig must take its non-interactive arm rather than
	// prompting: an unknown, unset key reports that it is not
	// configured instead of asking for a value.
	if err := requireConfig("suppressed_group_that_does_not_exist.key"); err == nil {
		t.Error("requireConfig succeeded while suppressed; it prompted or invented a value")
	}

	restore()
	if !isInteractive() {
		t.Error("isInteractive() = false after restore; suppression leaked past the command")
	}
}

// TestSuppressPromptsNests guards the restore contract: the returned
// function puts back what was there rather than unconditionally
// re-enabling prompts, so an inner suppression finishing cannot lift an
// outer one that is still in force.
func TestSuppressPromptsNests(t *testing.T) {
	t.Cleanup(ui.ResetStreams)
	ui.SetStreams(strings.NewReader(""), io.Discard, io.Discard)

	outer := suppressPrompts()
	inner := suppressPrompts()

	inner()
	if isInteractive() {
		t.Error("inner restore re-enabled prompting inside an outer suppression")
	}

	outer()
	if !isInteractive() {
		t.Error("outer restore left prompting off")
	}
}
