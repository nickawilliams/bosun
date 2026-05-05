# UI Components

The terminal output vocabulary for bosun. Each component exists
because a concrete command needs it. Components are described by
their semantic shape — what they contain, what states they have,
what they're for. Visual representation (borders, glyphs, colors)
is in the implementation.

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
- A Plan Card with confirmation enabled **requires `--yes`** to
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
  commands and rendering. `cardReporter` is the interactive
  implementation; `rawReporter` suppresses all output. Commands
  emit through `Reporter` methods; the active implementation
  decides how to present.
- **`Card`** (`card.go`) — the rendering primitive. All timeline
  output flows through Card: state glyph, title, subtitle, body
  variants (text, muted, KV, stdout, stderr, raw).
- **`Slot`** (`slot.go`) — manages a single timeline position where
  cards replace each other in sequence (show/run/clear/finalize).
- **Form layout** (`form_layout.go`) — regex post-processing that
  fixes huh's field-separator alignment. Non-obvious; the comment
  in the file explains why.
