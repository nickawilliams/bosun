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

	name := cmd.Name()

	fmt.Fprintln(w, ".nh")
	fmt.Fprintf(w, ".TH %q %q %q %q %q\n",
		strings.ToUpper(name), opts.Section, opts.Date, opts.Source, opts.Manual)

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, ".SH NAME")
	fmt.Fprintf(w, "%s \\- %s\n", troffEscape(name), troffEscape(cmd.Short))

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, ".SH SYNOPSIS")
	fmt.Fprintf(w, "\\fB%s\\fP [\\fIflags\\fP] [\\fIcommand\\fP]\n", troffEscape(name))

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, ".SH DESCRIPTION")
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	fmt.Fprintln(w, troffEscape(desc))

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, ".SH OPTIONS")
	seen := map[string]bool{}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || seen[flag.Name] {
			return
		}
		seen[flag.Name] = true
		writeManOption(w, flag)
	})
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || seen[flag.Name] {
			return
		}
		seen[flag.Name] = true
		writeManOption(w, flag)
	})
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, "\\fB\\-h\\fP, \\fB\\-\\-help\\fP")
	fmt.Fprintln(w, "Display help and exit.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, "\\fB\\-v\\fP, \\fB\\-\\-version\\fP")
	fmt.Fprintln(w, "Display version and exit.")

	subs := visibleSubcommands(cmd)
	if len(subs) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, ".SH COMMANDS")
		for _, sub := range subs {
			fmt.Fprintln(w, ".TP")
			fmt.Fprintf(w, "\\fB%s\\fP\n", troffEscape(sub.Name()))
			short := sub.Short
			if short == "" {
				short = "(no description)"
			}
			fmt.Fprintln(w, troffEscape(short))
		}
	}

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, ".SH EXIT STATUS")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, "0")
	fmt.Fprintln(w, "Success.")
	fmt.Fprintln(w, ".TP")
	fmt.Fprintln(w, "1")
	fmt.Fprintln(w, "An error occurred.")

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, ".SH SEE ALSO")
	fmt.Fprintln(w, "\\fBbash\\fP(1), \\fBzsh\\fP(1), \\fBfish\\fP(1)")

	return nil
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

func writeManOption(w io.Writer, flag *pflag.Flag) {
	fmt.Fprintln(w, ".TP")

	var sig strings.Builder
	if flag.Shorthand != "" && flag.ShorthandDeprecated == "" {
		fmt.Fprintf(&sig, "\\fB\\-%s\\fP, ", flag.Shorthand)
	}
	fmt.Fprintf(&sig, "\\fB\\-\\-%s\\fP", troffEscape(flag.Name))
	if flag.Value.Type() != "bool" {
		fmt.Fprintf(&sig, "=\\fI%s\\fP", strings.ToUpper(flag.Name))
	}
	fmt.Fprintln(w, sig.String())

	usage := flag.Usage
	def := flag.DefValue
	if def != "" && def != "false" && def != "0" && def != `""` {
		fmt.Fprintf(w, "%s (default: %s).\n", capitalizeFirst(usage), def)
	} else {
		fmt.Fprintln(w, capitalizeFirst(usage)+".")
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
