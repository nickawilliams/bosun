// Package generate produces shipping artifacts (man pages, shell completions)
// from bosun's cobra command tree.
package generate

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ManPageOptions configures man page generation.
type ManPageOptions struct {
	// Section is the man page section number (default "1").
	Section string
	// Date is the date in the .TH header (default current "Jan 2006").
	Date string
	// Source labels the binary in the .TH header (default cmd.Name()).
	Source string
	// Manual labels the manual section in the .TH header (default "User Commands").
	Manual string
}

// WriteManPage writes a troff man page for the cobra command tree rooted at cmd.
func WriteManPage(w io.Writer, cmd *cobra.Command, opts ManPageOptions) error {
	if opts.Section == "" {
		opts.Section = "1"
	}
	if opts.Date == "" {
		opts.Date = time.Now().Format("Jan 2006")
	}
	if opts.Source == "" {
		opts.Source = cmd.Name()
	}
	if opts.Manual == "" {
		opts.Manual = "User Commands"
	}

	ew := &errWriter{w: w}
	name := cmd.Name()

	ew.println(".nh")
	ew.printf(".TH %q %q %q %q %q\n",
		strings.ToUpper(name), opts.Section, opts.Date, opts.Source, opts.Manual)

	ew.println("")
	ew.println(".SH NAME")
	ew.printf("%s \\- %s\n", troffEscape(name), troffEscape(cmd.Short))

	ew.println("")
	ew.println(".SH SYNOPSIS")
	ew.printf("\\fB%s\\fP [\\fIflags\\fP] [\\fIcommand\\fP]\n", troffEscape(name))

	ew.println("")
	ew.println(".SH DESCRIPTION")
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	ew.println(troffEscape(desc))

	ew.println("")
	ew.println(".SH OPTIONS")
	seen := map[string]bool{}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || seen[flag.Name] {
			return
		}
		seen[flag.Name] = true
		writeManOption(ew, flag)
	})
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || seen[flag.Name] {
			return
		}
		seen[flag.Name] = true
		writeManOption(ew, flag)
	})
	ew.println(".TP")
	ew.println("\\fB\\-h\\fP, \\fB\\-\\-help\\fP")
	ew.println("Display help and exit.")
	ew.println(".TP")
	ew.println("\\fB\\-v\\fP, \\fB\\-\\-version\\fP")
	ew.println("Display version and exit.")

	subs := visibleSubcommands(cmd)
	if len(subs) > 0 {
		ew.println("")
		ew.println(".SH COMMANDS")
		for _, sub := range subs {
			ew.println(".TP")
			ew.printf("\\fB%s\\fP\n", troffEscape(sub.Name()))
			short := sub.Short
			if short == "" {
				short = "(no description)"
			}
			ew.println(troffEscape(short))
		}
	}

	ew.println("")
	ew.println(".SH EXIT STATUS")
	ew.println(".TP")
	ew.println("0")
	ew.println("Success.")
	ew.println(".TP")
	ew.println("1")
	ew.println("An error occurred.")

	ew.println("")
	ew.println(".SH SEE ALSO")
	ew.println("\\fBbash\\fP(1), \\fBzsh\\fP(1), \\fBfish\\fP(1)")

	return ew.err
}

func visibleSubcommands(cmd *cobra.Command) []*cobra.Command {
	var subs []*cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		subs = append(subs, sub)
	}
	return subs
}

func writeManOption(ew *errWriter, flag *pflag.Flag) {
	ew.println(".TP")

	var sig strings.Builder
	if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
		fmt.Fprintf(&sig, "\\fB\\-%s\\fP, ", flag.Shorthand)
	}
	fmt.Fprintf(&sig, "\\fB\\-\\-%s\\fP", troffEscape(flag.Name))
	if flag.Value.Type() != "bool" {
		fmt.Fprintf(&sig, "=\\fI%s\\fP", strings.ToUpper(flag.Name))
	}
	ew.println(sig.String())

	usage := flag.Usage
	def := flag.DefValue
	if def != "" && def != "false" && def != "0" && def != `""` {
		ew.printf("%s (default: %s).\n", capitalizeFirst(usage), def)
	} else {
		ew.println(capitalizeFirst(usage) + ".")
	}
}

// errWriter accumulates the first write error so callers don't need to check
// every fmt.Fprint* call individually.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) printf(format string, args ...any) {
	if ew.err == nil {
		_, ew.err = fmt.Fprintf(ew.w, format, args...)
	}
}

func (ew *errWriter) println(args ...any) {
	if ew.err == nil {
		_, ew.err = fmt.Fprintln(ew.w, args...)
	}
}

func troffEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "-", "\\-")
	return s
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
