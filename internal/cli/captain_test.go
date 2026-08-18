package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// TestPrintCaptainArtFinalizesTheOpenCard locks the one thing about
// the easter egg that can break real output: it writes raw rows
// straight to stdout, below whatever card the timeline currently has
// open. Without the finalize, that card keeps a blank gutter while the
// art sits under it, and the next card printed would rewrite the card
// and clear the art along with it.
//
// The assertion is on the escape rather than the re-rendered card,
// since the continuing form is private to the ui package: the art must
// be preceded by a cursor-up-and-clear, which is the swap happening.
func TestPrintCaptainArtFinalizesTheOpenCard(t *testing.T) {
	// Pin a card reporter: a raw one left installed by an earlier test
	// in this package would suppress Card.Print entirely.
	prev := ui.Default()
	ui.SetDefault(ui.NewCardReporter())
	t.Cleanup(func() { ui.SetDefault(prev) })
	ui.DiscardOpenCard()
	t.Cleanup(ui.DiscardOpenCard)

	// Two lines tall, so the rewrite moves up exactly two.
	card := ui.NewCard(ui.CardSuccess, "orders").Muted("body line")

	out := captureProcessStdout(t, func() {
		card.Print()
		printCaptainArt()
	})

	const rewrite = "\x1b[2F\x1b[J"
	swap := strings.Index(out, rewrite)
	art := strings.Index(out, "@@@@")
	switch {
	case art < 0:
		t.Fatalf("the art itself did not render:\n%q", out)
	case swap < 0:
		t.Errorf("the open card's spine was never restored:\n%q", out)
	case swap > art:
		t.Error("the spine was restored after the art, not before it")
	}
}

// captureProcessStdout redirects os.Stdout for the duration of fn. The
// card printers and printCaptainArt both write through fmt's
// package-level helpers, so the process stream is the only seam.
func captureProcessStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()
	defer func() {
		os.Stdout = orig
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	return <-read
}
