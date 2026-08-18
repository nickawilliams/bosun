// Package ui — state grammar.
//
// This file is the central reference for how bosun encodes "what
// state is this thing in" visually. Every glyph-and-color choice in
// the timeline should follow these rules; deviations are bugs.
//
// The glyphs below are palette fields, not literals — ✓ is
// Palette.Check, ● is Palette.Active, and so on (see theme.go).
// Write the field, never the character: a literal ✓ at a call site
// is a duplicate that a future glyph mode would silently fail to
// swap. Box-drawing chrome is the deliberate exception; it lives in
// glyphs.go and is fixed.
//
// The grammar splits into two CONTEXTS. Pick the context first, then
// the row. Most rows are unambiguous; if you can't decide, the
// fallback is "state context" (the larger category).
//
//	┌──────────────────────────────────────────────────────────────────┐
//	│  STATE CONTEXT  —  describes what an aspect currently IS         │
//	│  Used by: Status command, lifecycle indicators, repo cards,      │
//	│           anything that says "here is the present state of X"   │
//	└──────────────────────────────────────────────────────────────────┘
//
//	  ●  <color>     Active state. Color carries the role:
//	     green   (RoleOpen)      — alive, healthy, progressing
//	     yellow  (RoleAttention) — needs intervention
//	     blue    (RoleInFlight)  — transitioning right now
//	     muted   (RoleNeutral)   — unknown / unset / draft
//	     red       — broken state (rare for ● — prefer ✗ if terminal)
//
//	  ✓  purple   (RoleDone)      Terminal positive resolution. The
//	                              aspect is finalized successfully,
//	                              nothing more for the user to do.
//	                              (PR merged, issue closed-as-done.)
//
//	  ✗  red      (RoleClosed)    Terminal negative resolution. The
//	                              aspect ended without success.
//	                              (PR closed without merge, abandoned.)
//
//	  ▲  yellow   (RoleAttention) Needs the user's attention NOW.
//	                              Reserve for blocking/urgent — prefer
//	                              ● yellow for informational warnings.
//
//	  ⧗  blue     (RoleInFlight)  Live process visible in this frame.
//	                              Spinners only — for "lifecycle is
//	                              in_progress" (a state, not a live
//	                              process) use ● blue.
//
//	  ?  muted    (RoleNeutral)   Outcome can't be determined.
//
//	┌──────────────────────────────────────────────────────────────────┐
//	│  EVENT CONTEXT  —  describes what just HAPPENED                  │
//	│  Used by: Doctor checks, post-action result rows, anything       │
//	│           that says "we just did X and here's how it went"      │
//	└──────────────────────────────────────────────────────────────────┘
//
//	  ✓  green    (Success)        Action just completed successfully.
//	  ✗  red      (Error)          Action just failed.
//	  ⧗  blue     (Info)           Action in progress (spinner).
//	  ▲  yellow   (Warning)        Action warns / needs attention.
//	  ?  muted    (Muted)          Action outcome unknown.
//
// THE DECISION RULE:
//
//	For each row, ask two questions:
//	  1. Is this aspect locked in, or could it still change?
//	     locked → ✓ / ✗   |   active → ●   |   live → ⧗
//	  2. Which role / severity does the current value play?
//	     → pick the color
//
// PARALLEL VOCABULARIES, ONE GRAMMAR:
//
//	The Palette holds two color vocabularies that alias the same
//	underlying colors:
//	  - Severity-flavored: Success / Warning / Error / Info / Muted
//	    — used at event-context call sites
//	  - Resolution-role: RoleOpen / RoleDone / RoleClosed /
//	    RoleAttention / RoleInFlight / RoleNeutral
//	    — used at state-context call sites
//	The names tell a reader (and grep) which grammar applies.
//	Mixing them at one call site is a smell — pick a context.
//
// WHY GitHub-PRIMER-STYLE ROLES, NOT JUST SEVERITY:
//
//	GitHub's PR vocabulary encodes "open / done / closed" as a
//	distinct axis from severity — merged-PR purple is a different
//	signal than green ✓ on a passing check, because one says "no
//	more action" and the other says "this action succeeded just
//	now". Borrowing that axis lets us preserve the scannable green
//	✓ for events while keeping a clean purple ✓ for terminal-done
//	states. See state_grammar.go's commit message for the rationale.
package ui
