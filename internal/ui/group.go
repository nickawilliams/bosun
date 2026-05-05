package ui

import (
	"fmt"
	"strings"
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

// --- Message types (callback goroutine → BubbleTea model) ---

type groupMsg interface{ groupMsg() }

type groupChildMsg struct {
	rendered string
	state    CardState
}

func (groupChildMsg) groupMsg() {}

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
type group struct {
	outer  Reporter
	title  string
	indent int // children's indent depth
	msgCh  chan<- groupMsg
	counts groupCounts
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
	g.msgCh <- groupChildMsg{rendered: c.Render(), state: state}
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

func (g *group) Skip(label string) {
	g.sendChild(CardSkipped, NewCard(CardSkipped, label))
}

func (g *group) Fail(label string) {
	g.sendChild(CardFailed, NewCard(CardFailed, label))
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
		state := CardSuccess
		if err != nil {
			card.state = CardFailed
			card.Subtitle(err.Error())
			state = CardFailed
		}
		doneCh <- err
		g.msgCh <- groupTaskDoneMsg{err: err, rendered: card.Render(), state: state}
	}()
	err := <-doneCh
	if err != nil {
		g.counts.failed++
	} else {
		g.counts.success++
	}
	return err
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

func (g *group) Group(title string, fn func(Reporter)) {
	g.msgCh <- groupBeginMsg{title: title, indent: g.indent}
	inner := &group{
		outer:  g.outer,
		title:  title,
		indent: g.indent + 1,
		msgCh:  g.msgCh,
	}
	fn(inner)
	g.msgCh <- groupEndMsg{}
	g.counts.success++ // sub-group contributes to parent aggregate
}

// aggregate computes the final CardState from child outcomes.
func (g *group) aggregate() CardState {
	return aggregateCounts(g.counts)
}

func aggregateCounts(c groupCounts) CardState {
	if c.failed > 0 {
		return CardFailed
	}
	if c.success > 0 {
		return CardSuccess
	}
	if c.skipped > 0 {
		return CardSkipped
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
	rendered string     // pre-rendered card, or empty for sub-groups
	node     *groupNode // non-nil for sub-group children
}

type groupActiveTask struct {
	title  string
	indent int
}

type groupModel struct {
	spinner spinner.Model
	msgCh   <-chan groupMsg
	root    *groupNode
	current *groupNode
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
		m.current.children = append(m.current.children, groupRenderedChild{rendered: msg.rendered})
		switch msg.state {
		case CardSuccess:
			m.current.counts.success++
		case CardSkipped:
			m.current.counts.skipped++
		case CardFailed:
			m.current.counts.failed++
		case CardInfo:
			m.current.counts.info++
		}

	case groupTaskStartMsg:
		m.current.activeTask = &groupActiveTask{title: msg.title, indent: msg.indent}

	case groupTaskDoneMsg:
		if m.current.activeTask != nil {
			m.current.children = append(m.current.children, groupRenderedChild{rendered: msg.rendered})
			m.current.activeTask = nil
			if msg.err != nil {
				m.current.counts.failed++
			} else {
				m.current.counts.success++
			}
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
		if child.node != nil {
			m.renderNode(b, child.node)
		} else {
			b.WriteString(child.rendered)
		}
	}

	if node.activeTask != nil {
		taskCard := NewCard(CardRunning, node.activeTask.title).Indent(node.activeTask.indent)
		taskCard.tight = true
		b.WriteString(taskCard.renderWithGlyph(m.spinner.View()))
	}
}

