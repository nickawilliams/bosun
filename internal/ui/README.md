# UI Components

The terminal output vocabulary for bosun. Each component exists
because a concrete command needs it. Components are described by
their semantic shape — what they contain, what states they have,
what they're for. Visual representation (borders, glyphs, colors)
is in the implementation:

- `theme.go` — the palette: colors and the themeable symbol set
  (`✓ ✗ ● ▲ ⧗ ? ◦ ○` plus `→ • ·`). Read these fields rather than
  writing the literal.
- `state_grammar.go` — which glyph and color pair with which state.
- `glyphs.go` — box-drawing chrome (`│ ─ ╭ ╮ ╯ ╰ ├ └`). Fixed, not
  themeable: it encodes no state and rule widths are computed
  against it.
- `timeline.go` — the open/continuing form swap that terminates a run
  of cards (see "Timeline termination" below).

Run `bosun demo` for a static reference of all components, or
`bosun demo --interactive` for a live walkthrough with spinners,
forms, and animated elements.

## Application modes

The application chooses how to render a command's output:

- **Interactive** — heading + timeline of components.
- **Raw / machine-readable** — bypass the timeline; commands emit
  data directly to stdout (e.g. `config get`, `config show --output
  json`). Components don't render in this mode.

**Mode selection.** Auto-detect: interactive when stdout is a TTY,
raw otherwise. Explicit flags (e.g. `--output json`) or command
annotations (`"output": "raw"`) override.

**Raw-mode rules:**

- A command that needs input but has no flag value to use **errors
  with a missing-flag message**. No silent guessing.
- A Plan Card with confirmation enabled **requires `--approve`** to
  apply. Without it, the command errors before applying. `--dry-run`
  is always safe in raw mode (no mutation).
- Errors are written to stderr as plain text (`error: <message>`).

## Components

### Heading

A decorated application header (`CardRoot`). One per command
invocation.

- **Sub-command format**: nested via separators, e.g.
  `Bosun > Workspace > Create`. Built from the cobra command
  hierarchy by `cli/header.go`.
- **Hidden commands** (`demo`, `captain`) get the same heading style.
- **Breadcrumb structure** (what segments are included, what the
  terminal segment represents, when modes get qualified) is
  defined in `internal/cli/README.md` — see "Heading & breadcrumb
  structure". The UI layer renders whatever the cli layer
  declares.

#### Absorption ("squish")

The first non-root card emitted after a Heading is "absorbed" —
its title is appended to the heading's breadcrumb as the terminal
segment. Subtitle/body content renders below the box.

- **State glyph** of the absorbed card prefixes its segment by
  default (e.g. ✓ for `CardSuccess`); suppress with
  `Card.HideAbsorbedGlyph()` when the segment is purely
  informational data.
- **Title color** is `Palette.Primary` by default; override with
  `Card.AbsorbedTitleColor(c)` to mark the segment as a data
  identifier (convention: `Palette.Success` / green).
- **`RunCard` family** absorbs too: the spinner animates in the
  segment position during the task, then resolves to the success
  card's title + glyph (or a ✓ on the running title if no
  replacement card was provided).
- **Inert override**: emit a card with no title and no body to
  consume the squish slot without modifying the breadcrumb.

See `internal/ui/squish.go` for the state machine.

### Timeline Card

A line in a vertical timeline, representing one outcome (`Card`).

- **States**: pending (spinner) -> finalized (success, failure,
  skipped, info)
- **May contain children** via `Reporter.Group(title, func(g
  Reporter))`. A parent shows an animated spinner while children
  run, then finalizes to an aggregate state.
- **Aggregate status**: failure dominates; all-skipped -> skipped;
  any success (including success+skipped mix) -> success; info
  doesn't propagate.
- **Spinner timing floor**: 100ms minimum display duration prevents
  BubbleTea v2 terminal-mode-query escapes from leaking on fast
  operations.

#### Timeline termination

Cards render in one of two forms, and the timeline swaps between them
as it grows:

- **open** — the card is the last thing in the timeline. Body lines
  carry no spine, so the run visibly ends.
- **continuing** — a successor exists below. Body lines carry the
  spine, joining the card to what follows.

A card always prints open. When the next card prints, the previous one
is rewritten in place (cursor-up + clear-to-end) into continuing form.
`EndTimeline` deliberately does *not* rewrite: leaving the last card
open is what says "this is the end."

- **Glyphless cards** absorb into the timeline rather than leaving a
  hole in it — the spine becomes their marker: `├─` continuing, `╰─`
  open, with content still landing at `ContentCol(0)`.
- **Non-card output** that paints below a card without going through
  the spacer prefix (huh forms, raw hand-offs) must call
  `ui.FinalizeOpenCard()` first, so the spine is restored before the
  region is borrowed. `ui.DiscardOpenCard()` forgets the card without
  rewriting, for blocks about to be erased.
- **No-op swaps are skipped.** A card with no body renders the same
  either way, so it is never repainted — that covers most cards and
  the whole logo box.
- **Nested cards don't participate** (v1). An indented card's body
  sits inside its parent's spine; dropping its connector would punch
  a hole mid-group rather than terminate anything.
- **Raw mode** records and rewrites nothing — the escapes would
  corrupt a piped stream.

### Plan Card

A grouped collection of action rows representing work a mutating
command will (or would) perform (`Plan`, `PlanCard`).

- **Rows**: each has a verb (create/modify/destroy/no-change) and
  a label.
- **Lifecycle**: assessing -> proposed -> (confirmation?) -> (apply?)
- **Two independent control axes** (`PlanOpts`):
  - **Confirm**: on for lifecycle Phase.Plan commands; off for
    workspace direct mutating Tasks.
  - **Apply**: on by default; off for `--dry-run`.
- **No-work branch**: when assess yields nothing to do, the plan
  renders proposed and finalizes without confirmation or apply.
- **Confirmation denied**: all rows finalize as skipped.
- **Apply is best-effort, with one gate**: every queued action is
  attempted and the first error is returned at the end, so an
  independent row isn't held hostage to an unrelated failure. Actions
  that set `PlanAction.RequiresPriorSuccess` are the exception —
  they're skipped once anything *earlier* has failed (an earlier
  action's apply error, or a ✗ assess row that landed before they were
  queued, via `PlanAction.PriorFailure`). Failures after them don't
  count, so a notification queued behind a transition can't withhold
  it. That's for side effects asserting something
  about the run as a whole; the issue-tracker transition is the
  motivating case, since moving an issue to Done behind a failed
  deploy publishes a success that never happened. A skipped row
  renders with the skipped glyph and `(skipped)`, and denies the card
  its Success state.
- **Aggregate status**: same rules as Timeline Card.

### Input Card

A transient placeholder in the timeline that hosts a huh form
(`Slot`, `Card` with `CardInput` state).

- **Widget variants**: free-text, single-choice, multi-choice, yes/no
- **Default values use placeholder semantics** (`defaultField` /
  `newDefaultInput` in `cli/prompt.go`): the field shows the default
  as ghost text; blank submission accepts it.
- **Resolution**: on submit, either replaced by a finalized Timeline
  Card or removed entirely.
- **Ctrl+C** is process-level interrupt, not form-level cancel.

### Data Card

A finalized snapshot of structured state (`CardData`).

- **Body**: key-value list via `Reporter.Details(heading, fields)`.
- **No status glyph** — represents state, not an outcome.
- **Empty body** (zero fields): the card is suppressed entirely.

### Tree

A hierarchical, recursive display of labeled nodes (`Tree`,
`TreeNode`).

- Each node has a label, optionally a value, and optionally children.
- **No status semantics** — a snapshot of structure.
- Used by `config show` to render the resolved configuration tree.

## Key abstractions

- **`Reporter` interface** (`reporter.go`) — the seam between
  commands and rendering. Three implementations exist:
  - `cardReporter` — interactive TTY; full animated timeline.
  - `plainReporter` (`plain_reporter.go`) — non-TTY stdout without
    explicit structured-output request; emits plain, unstyled lines
    like `[ok] label: value` so piped, redirected, and CI contexts see
    the same semantic events as an interactive run.
  - `rawReporter` (`raw_reporter.go`) — machine-readable mode
    (`--output` flag or `output: raw` annotation); suppresses all
    timeline output so the command can write its own payload cleanly.
  - `CaptureReporter` (`capture_reporter.go`) — test-only; records
    calls instead of rendering them so tests can assert on what a
    command reported.
  `IsRaw()` covers every non-card implementation (plain, raw, and
  capture) — it enumerates them, so a new one needs a case added.
  The test harness installs the capture reporter via both
  `SetRawReporterFactory` and `SetPlainReporterFactory` rather than
  `SetDefault`, because bootstrap installs a fresh reporter on every
  command run.
- **`Card`** (`card.go`) — the rendering primitive. All timeline
  output flows through Card: state glyph, title, subtitle, body
  variants (text, muted, KV, stdout, stderr, raw).
- **`Slot`** (`slot.go`) — manages a single timeline position where
  cards replace each other in sequence (show/run/clear/finalize).
- **Form layout** (`form_layout.go`) — regex post-processing that
  fixes huh's field-separator alignment. Non-obvious; the comment
  in the file explains why.
