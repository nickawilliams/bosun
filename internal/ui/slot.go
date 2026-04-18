package ui

// Slot manages a single timeline position where cards replace each
// other in sequence. Instead of manually tracking rewind closures,
// the caller uses Show/Run/Clear/Finalize to drive the lifecycle.
//
//	slot := ui.NewSlot()
//	slot.Run("Fetching data", fetchFn)
//	slot.Show(ui.NewCard(ui.CardInput, "Pick one").Tight())
//	// ... form interaction ...
//	slot.Clear()
type Slot struct {
	rewind    func()
	finalized bool
}

// NewSlot creates an empty slot at the current timeline position.
func NewSlot() *Slot {
	return &Slot{}
}

// Run shows a spinner card while fn executes. On success the result
// card is rewindable — a subsequent Show, Run, or Clear replaces it.
// On failure the failure card is printed permanently by the underlying
// RunCardRewindable; the slot returns to empty state and the error is
// returned.
func (s *Slot) Run(title string, fn func() error) error {
	s.mustBeOpen()
	s.clear()
	rewind, err := RunCardRewindable(title, fn)
	if err != nil {
		return err
	}
	s.rewind = rewind
	return nil
}

// Show replaces the current display with a static card. The card
// remains rewindable until Clear or Finalize is called.
func (s *Slot) Show(card *Card) {
	s.mustBeOpen()
	s.clear()
	s.rewind = card.PrintRewindable()
}

// Clear erases whatever is currently displayed, leaving the timeline
// position empty. The caller typically prints a final card immediately
// after. Clearing an already-empty slot is a no-op.
func (s *Slot) Clear() {
	s.mustBeOpen()
	s.clear()
}

// Finalize makes the current display permanent. No further
// replacements are possible. Finalize is idempotent.
func (s *Slot) Finalize() {
	s.rewind = nil
	s.finalized = true
}

// clear is the single internal point where rewind invocation happens.
func (s *Slot) clear() {
	if s.rewind != nil {
		s.rewind()
		s.rewind = nil
	}
}

// mustBeOpen panics if the slot has been finalized.
func (s *Slot) mustBeOpen() {
	if s.finalized {
		panic("slot: use after finalize")
	}
}
