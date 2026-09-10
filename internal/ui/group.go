package ui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// RunGroup renders a Timeline Card with children. The parent header
// prints with an animated spinner; each child emitted from within fn
// renders indented under it in real-time. When fn returns, the parent's
// spinner is replaced with a glyph aggregated from its children:
//
//   - any failure → failure
//   - all skipped → skipped
//   - any success (including success+skipped mix) → success
//   - info-status emissions don't propagate to the aggregate
//
// The inner Reporter passed to fn is scoped to this group: its
// emissions are tracked for aggregation and indented under the
// parent. Nested RunGroup calls work — each level adds another
// 4 spaces of indent.
//
// Parent and child spinners animate simultaneously via a single
// BubbleTea program that manages all rendering for the group's
// lifetime.
func RunGroup(title string, fn func(g Reporter)) {
	defaultReporter.Group(title, fn)
}

// RunGroupRewindable is RunGroup returning a rewind that erases the
// finalized group card — for call sites that morph the card into
// content derived from the same rows (bulk cleanup's readiness-
// annotated picker; the emitDeploymentSources gather→form→record
// pattern). Returns nil when the render can't be rewound (raw /
// plain / capture reporters, or the non-TTY fallback); callers must
// treat nil as "leave the card standing".
func RunGroupRewindable(title string, fn func(g Reporter)) func() {
	if r, ok := defaultReporter.(*cardReporter); ok {
		return r.runGroup(title, fn)
	}
	defaultReporter.Group(title, fn)
	return nil
}

// --- Message types (callback goroutine → BubbleTea model) ---

type groupMsg interface{ groupMsg() }

type groupChildMsg struct {
	rendered string
	state    CardState
	slot     int // non-zero: replace that slot's running row in place
}

func (groupChildMsg) groupMsg() {}

// groupSlotStartMsg registers a concurrent child slot: a running-
// indicator row whose position in the group is fixed now, while its
// content resolves later — the mechanism behind FanOut's per-item
// spinners.
type groupSlotStartMsg struct {
	title  string
	indent int
	slot   int
}

func (groupSlotStartMsg) groupMsg() {}

// groupSlotDoneMsg clears a slot's running indicator once its
// resolved rows (slot-tagged groupChildMsg) have been emitted.
type groupSlotDoneMsg struct{ slot int }

func (groupSlotDoneMsg) groupMsg() {}

type groupTaskStartMsg struct {
	title  string
	indent int
}

func (groupTaskStartMsg) groupMsg() {}

type groupTaskDoneMsg struct {
	err      error
	rendered string
	state    CardState
}

func (groupTaskDoneMsg) groupMsg() {}

type groupBeginMsg struct {
	title  string
	indent int
}

func (groupBeginMsg) groupMsg() {}

type groupEndMsg struct{}

func (groupEndMsg) groupMsg() {}

type groupDoneMsg struct{}

func (groupDoneMsg) groupMsg() {}

// --- group: the Reporter implementation for callback goroutines ---

// group sends messages on msgCh; the BubbleTea model receives them
// and manages all rendering. The callback goroutine never writes
// directly to stdout.
//
// counts lives behind a pointer + mutex because slot-scoped clones
// (see Slot) share their parent's tally: FanOut resolves slots from
// worker goroutines, and each resolution's emissions must land on the
// one aggregate the group finalizes with.
type group struct {
	outer    Reporter
	title    string
	indent   int // children's indent depth
	msgCh    chan<- groupMsg
	slot     int // non-zero: emissions resolve this slot in place
	counts   *groupCounts
	countsMu *sync.Mutex
}

// newGroup constructs a group reporter with its own counts. Slot
// clones share these fields instead — see Slot.
func newGroup(outer Reporter, title string, indent int, msgCh chan<- groupMsg) *group {
	return &group{
		outer:    outer,
		title:    title,
		indent:   indent,
		msgCh:    msgCh,
		counts:   &groupCounts{},
		countsMu: &sync.Mutex{},
	}
}

type groupCounts struct {
	success int
	skipped int
	failed  int
	info    int
}

func (g *group) sendChild(state CardState, c *Card) {
	c.Indent(g.indent)
	c.tight = true
	// Children are list entries under the group's bold parent title,
	// not headings themselves — render their titles without bold so
	// the parent stays the row that carries the visual weight.
	c.plainTitle = true
	g.msgCh <- groupChildMsg{rendered: c.Render(), state: state, slot: g.slot}
	g.bumpCount(state)
}

// bumpCount tallies a child outcome into the shared aggregate.
// Mutex-guarded because slot clones emit from worker goroutines.
func (g *group) bumpCount(state CardState) {
	g.countsMu.Lock()
	defer g.countsMu.Unlock()
	switch state {
	case CardSuccess:
		g.counts.success++
	case CardSkipped:
		g.counts.skipped++
	case CardFailed:
		g.counts.failed++
	case CardInfo:
		g.counts.info++
	}
}

func (g *group) Header(_ string, _ ...string) {}

func (g *group) Complete(label string) {
	g.sendChild(CardSuccess, NewCard(CardSuccess, label))
}

func (g *group) CompleteDetail(label string, items []string) {
	g.sendChild(CardSuccess, NewCard(CardSuccess, label).Muted(items...))
}

func (g *group) CompleteValue(label, value string, alignWidth ...int) {
	g.sendChild(CardSuccess, alignedValueCard(CardSuccess, label, value, alignWidth))
}

func (g *group) Skip(label string) {
	g.sendChild(CardSkipped, NewCard(CardSkipped, label))
}

func (g *group) SkipValue(label, value string, alignWidth ...int) {
	g.sendChild(CardSkipped, alignedValueCard(CardSkipped, label, value, alignWidth))
}

func (g *group) Fail(label string) {
	g.sendChild(CardFailed, NewCard(CardFailed, label))
}

func (g *group) FailValue(label, value string, alignWidth ...int) {
	g.sendChild(CardFailed, alignedValueCard(CardFailed, label, value, alignWidth))
}

// alignedValueCard constructs a value-form card with optional title
// alignment. Pulled out so the three value-form group methods share
// the same construction logic.
func alignedValueCard(state CardState, label, value string, alignWidth []int) *Card {
	card := NewCard(state, label).Value(value)
	if len(alignWidth) > 0 && alignWidth[0] > 0 {
		card = card.AlignWidth(alignWidth[0])
	}
	return card
}

func (g *group) Success(format string, args ...any) {
	g.sendChild(CardSuccess, NewCard(CardSuccess, fmt.Sprintf(format, args...)))
}

func (g *group) Warning(format string, args ...any) {
	g.sendChild(CardSkipped, NewCard(CardSkipped, fmt.Sprintf(format, args...)))
}

func (g *group) Info(format string, args ...any) {
	g.sendChild(CardInfo, NewCard(CardInfo, fmt.Sprintf(format, args...)))
}

func (g *group) Muted(format string, args ...any) {
	g.sendChild(CardInfo, NewCard(CardInfo, fmt.Sprintf(format, args...)))
}

func (g *group) DryRun(format string, args ...any) {
	g.sendChild(CardInfo, NewCard(CardInfo, fmt.Sprintf("[dry-run] "+format, args...)))
}

func (g *group) Saved(label, value string) {
	g.sendChild(CardSuccess, NewCard(CardSuccess, label).Muted(value))
}

func (g *group) Selected(label, value string) {
	g.sendChild(CardSuccess, NewCard(CardSuccess, label).Subtitle(value))
}

func (g *group) SelectedIdentifier(label, value string) {
	g.sendChild(CardSuccess, NewCard(CardSuccess, label).PreserveCase().Subtitle(value))
}

func (g *group) SelectedMulti(label string, values []string) {
	if len(values) == 0 {
		g.sendChild(CardSuccess, NewCard(CardSuccess, label).Subtitle("(none)"))
		return
	}
	g.sendChild(CardSuccess, NewCard(CardSuccess, label).Muted(values...))
}

func (g *group) Task(title string, fn func() error) error {
	doneCh := make(chan error, 1)
	g.msgCh <- groupTaskStartMsg{title: title, indent: g.indent}
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)

		card := NewCard(CardSuccess, title).Indent(g.indent)
		card.tight = true
		card.plainTitle = true
		state := CardSuccess
		if err != nil {
			card.state = CardFailed
			card.Subtitle(err.Error())
			state = CardFailed
		}
		// The done message must land on msgCh BEFORE the caller
		// unblocks: once doneCh is sent, the caller's next Task/Spinner
		// pushes its start message, and a stale done arriving after it
		// would clear the NEW task's spinner row — or silently drop a
		// later task's result card via the activeTask guard.
		g.msgCh <- groupTaskDoneMsg{err: err, rendered: card.Render(), state: state}
		doneCh <- err
	}()
	err := <-doneCh
	if err != nil {
		g.bumpCount(CardFailed)
	} else {
		g.bumpCount(CardSuccess)
	}
	return err
}

// Spinner shows an inline running indicator at the child level for
// title while fn executes, then clears the indicator without emitting
// any terminal card — the caller renders the final result. Counts
// are NOT incremented; the caller's subsequent Complete/Fail/Skip
// (or value-form variant) drives count aggregation.
func (g *group) Spinner(title string, fn func() error) error {
	doneCh := make(chan error, 1)
	g.msgCh <- groupTaskStartMsg{title: title, indent: g.indent}
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		// Empty rendered string signals the message handler to clear
		// the active task without appending a child card. Sent before
		// doneCh for the same ordering reason as Task: the caller's
		// next start message must not beat this done message to msgCh.
		g.msgCh <- groupTaskDoneMsg{err: err, rendered: "", state: CardSuccess}
		doneCh <- err
	}()
	return <-doneCh
}

func (g *group) Details(heading string, fields Fields) {
	if len(fields) == 0 {
		return
	}
	pairs := make([]string, 0, len(fields)*2)
	for _, f := range fields {
		pairs = append(pairs, f.Key, f.Value)
	}
	if heading == "" {
		heading = "Details"
	}
	g.sendChild(CardData, NewCard(CardData, heading).KV(pairs...))
}

func (g *group) Summary(total string, segments []SummarySegment) {
	g.sendChild(CardInfo, NewCard(CardInfo, renderSummaryText(total, segments)).
		PreserveCase().
		GlyphColor(summaryGlyphColor(segments)))
}

func (g *group) Group(title string, fn func(Reporter)) {
	g.msgCh <- groupBeginMsg{title: title, indent: g.indent}
	inner := newGroup(g.outer, title, g.indent+1, g.msgCh)
	fn(inner)
	g.msgCh <- groupEndMsg{}
	g.bumpCount(CardSuccess) // sub-group contributes to parent aggregate
}

// slotSeq issues slot ids. Package-global so ids stay unique across
// nested groups sharing one message channel; 0 is reserved for
// "no slot" in groupChildMsg.
var slotSeq atomic.Int64

// Slot starts a concurrent child slot: a running-indicator row for
// title whose position in the group is fixed immediately, without
// blocking the caller. The returned Reporter is slot-scoped — its
// emissions replace the running row in place — and done clears the
// indicator; call it exactly once, after the resolving emissions.
//
// Emissions on slot-scoped reporters may come from worker goroutines,
// but each slot's resolve-then-done sequence must not interleave with
// another's (FanOut serializes them) so multi-row resolutions stay
// contiguous in the rendered group.
func (g *group) Slot(title string) (Reporter, func()) {
	id := int(slotSeq.Add(1))
	g.msgCh <- groupSlotStartMsg{title: title, indent: g.indent, slot: id}
	scoped := &group{
		outer:    g.outer,
		title:    g.title,
		indent:   g.indent,
		msgCh:    g.msgCh,
		slot:     id,
		counts:   g.counts,
		countsMu: g.countsMu,
	}
	return scoped, func() { g.msgCh <- groupSlotDoneMsg{slot: id} }
}

// aggregate computes the final CardState from child outcomes.
func (g *group) aggregate() CardState {
	g.countsMu.Lock()
	defer g.countsMu.Unlock()
	return aggregateCounts(*g.counts)
}

func aggregateCounts(c groupCounts) CardState {
	// Worst child state dominates: failures first, then skipped/warn
	// (so warnings propagate visibly to the group glyph even when
	// most children succeeded), then success. The empty group falls
	// through to CardSuccess as a no-op default.
	if c.failed > 0 {
		return CardFailed
	}
	if c.skipped > 0 {
		return CardSkipped
	}
	if c.success > 0 {
		return CardSuccess
	}
	return CardSuccess
}

// --- BubbleTea model ---

// groupNode tracks the state of one group (parent + children) in the
// render tree. Nested groups push/pop via a parent pointer.
type groupNode struct {
	title      string
	indent     int
	children   []groupRenderedChild
	activeTask *groupActiveTask
	counts     groupCounts
	parent     *groupNode
	finalized  bool
	finalState CardState
}

type groupRenderedChild struct {
	rendered string          // pre-rendered card, or empty for sub-groups
	node     *groupNode      // non-nil for sub-group children
	slot     *groupSlotChild // non-nil for concurrent slot children
}

type groupActiveTask struct {
	title  string
	indent int
}

// groupSlotChild is one concurrent slot's render state: a running-
// indicator row that later resolves, in place, to the rows emitted
// through its slot-scoped reporter.
type groupSlotChild struct {
	title    string
	indent   int
	running  bool
	rendered []string
	owner    *groupNode // node whose counts the slot's rows tally into
}

type groupModel struct {
	spinner spinner.Model
	msgCh   <-chan groupMsg
	root    *groupNode
	current *groupNode
	slots   map[int]*groupSlotChild
	done    bool
}

func newGroupModel(title string, indentLevel int, msgCh <-chan groupMsg) *groupModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	root := &groupNode{title: title, indent: indentLevel}
	return &groupModel{
		spinner: s,
		msgCh:   msgCh,
		root:    root,
		current: root,
		slots:   map[int]*groupSlotChild{},
	}
}

func (m *groupModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForMsg())
}

func (m *groupModel) waitForMsg() tea.Cmd {
	return func() tea.Msg { return <-m.msgCh }
}

func (m *groupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Group messages from the callback goroutine.
	if gm, ok := msg.(groupMsg); ok {
		m.processMsg(gm)
		// Drain any additional pending messages so they render in
		// the same frame. This eliminates the one-tick delay between
		// sequential messages (e.g. taskDone immediately followed by
		// groupDone when the task was the last operation).
		for {
			select {
			case next := <-m.msgCh:
				m.processMsg(next)
			default:
				if m.done {
					return m, tea.Quit
				}
				return m, tea.Batch(m.spinner.Tick, m.waitForMsg())
			}
		}
	}

	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// processMsg applies a single group message to the model state.
func (m *groupModel) processMsg(msg groupMsg) {
	switch msg := msg.(type) {
	case groupChildMsg:
		target := m.current
		if msg.slot != 0 {
			// Slot-tagged: the row resolves an existing slot in place
			// rather than appending at the tail. Counts tally into the
			// slot's owning node — resolution can arrive after the
			// current pointer has notionally moved.
			sc := m.slots[msg.slot]
			if sc == nil {
				break // unknown slot — drop rather than misplace
			}
			sc.rendered = append(sc.rendered, msg.rendered)
			target = sc.owner
		} else {
			target.children = append(target.children, groupRenderedChild{rendered: msg.rendered})
		}
		switch msg.state {
		case CardSuccess:
			target.counts.success++
		case CardSkipped:
			target.counts.skipped++
		case CardFailed:
			target.counts.failed++
		case CardInfo:
			target.counts.info++
		}

	case groupSlotStartMsg:
		sc := &groupSlotChild{
			title:   msg.title,
			indent:  msg.indent,
			running: true,
			owner:   m.current,
		}
		m.slots[msg.slot] = sc
		m.current.children = append(m.current.children, groupRenderedChild{slot: sc})

	case groupSlotDoneMsg:
		if sc := m.slots[msg.slot]; sc != nil {
			sc.running = false
		}

	case groupTaskStartMsg:
		m.current.activeTask = &groupActiveTask{title: msg.title, indent: msg.indent}

	case groupTaskDoneMsg:
		if m.current.activeTask != nil {
			// Empty rendered = spinner-only (group.Spinner). Clear the
			// active task and skip both the child append and the count
			// update — the caller will emit its own terminal card via a
			// follow-up Complete/Fail/Skip (or value-form variant),
			// which handles counts through its own message path.
			if msg.rendered != "" {
				m.current.children = append(m.current.children, groupRenderedChild{rendered: msg.rendered})
				if msg.err != nil {
					m.current.counts.failed++
				} else {
					m.current.counts.success++
				}
			}
			m.current.activeTask = nil
		}

	case groupBeginMsg:
		child := &groupNode{
			title:  msg.title,
			indent: msg.indent,
			parent: m.current,
		}
		m.current.children = append(m.current.children, groupRenderedChild{node: child})
		m.current = child

	case groupEndMsg:
		m.current.finalized = true
		m.current.finalState = aggregateCounts(m.current.counts)
		if m.current.parent != nil {
			m.current.parent.counts.success++
			m.current = m.current.parent
		}

	case groupDoneMsg:
		m.root.finalized = true
		m.root.finalState = aggregateCounts(m.root.counts)
		m.done = true
	}
}

// viewString renders the group tree to a plain string — the shared
// render for the standalone program's View and the session shell's
// tail composition.
func (m *groupModel) viewString() string {
	var b strings.Builder
	m.renderNode(&b, m.root)
	return b.String()
}

func (m *groupModel) View() tea.View {
	// Always render the current state — including the final frame.
	// When done, the root is finalized so renderNode uses aggregate
	// glyphs instead of spinners. BubbleTea's last render replaces
	// the animated content with the final static content in one
	// frame, avoiding a visible clear-then-reprint flash.
	var b strings.Builder
	m.renderNode(&b, m.root)
	return tea.NewView(b.String())
}

func (m *groupModel) renderNode(b *strings.Builder, node *groupNode) {
	parentCard := NewCard(CardPending, node.title).Indent(node.indent)
	if node.finalized {
		parentCard.state = node.finalState
		b.WriteString(parentCard.Render())
	} else {
		b.WriteString(parentCard.renderWithGlyph(m.spinner.View()))
	}

	for _, child := range node.children {
		switch {
		case child.node != nil:
			m.renderNode(b, child.node)
		case child.slot != nil:
			m.renderSlot(b, child.slot)
		default:
			b.WriteString(child.rendered)
		}
	}

	if node.activeTask != nil {
		taskCard := NewCard(CardRunning, node.activeTask.title).Indent(node.activeTask.indent)
		taskCard.tight = true
		taskCard.plainTitle = true
		b.WriteString(taskCard.renderWithGlyph(m.spinner.View()))
	}
}

// renderSlot renders one concurrent slot: its resolved rows once
// emitted, otherwise the running-indicator row at the slot's fixed
// position.
func (m *groupModel) renderSlot(b *strings.Builder, sc *groupSlotChild) {
	if len(sc.rendered) > 0 {
		for _, r := range sc.rendered {
			b.WriteString(r)
		}
		return
	}
	if sc.running {
		taskCard := NewCard(CardRunning, sc.title).Indent(sc.indent)
		taskCard.tight = true
		taskCard.plainTitle = true
		b.WriteString(taskCard.renderWithGlyph(m.spinner.View()))
	}
}
