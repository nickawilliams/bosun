package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

var (
	helpHeading = lipgloss.NewStyle().
			Bold(true).
			Foreground(ui.Palette.Primary)

	helpCommand = lipgloss.NewStyle().
			Foreground(ui.Palette.Accent)

	helpFlag = lipgloss.NewStyle().
			Foreground(ui.Palette.Accent)

	helpDim = lipgloss.NewStyle().
		Foreground(ui.Palette.Muted)
)

// setStyledHelp configures the command tree to use styled help output.
func setStyledHelp(cmd *cobra.Command) {
	cmd.SetHelpFunc(styledHelp)
}

func styledHelp(cmd *cobra.Command, args []string) {
	var b strings.Builder

	// Description.
	if cmd.Long != "" {
		b.WriteString(cmd.Long)
	} else if cmd.Short != "" {
		b.WriteString(cmd.Short)
	}
	b.WriteString("\n\n")

	// Usage.
	b.WriteString(helpHeading.Render("Usage"))
	b.WriteString("\n")
	if cmd.Runnable() {
		b.WriteString("  " + helpDim.Render(cmd.UseLine()) + "\n")
	}
	if cmd.HasAvailableSubCommands() {
		b.WriteString("  " + helpDim.Render(cmd.CommandPath()+" [command]") + "\n")
	}
	b.WriteString("\n")

	// Subcommands.
	if cmd.HasAvailableSubCommands() {
		b.WriteString(helpHeading.Render("Commands"))
		b.WriteString("\n")

		maxLen := 0
		for _, sub := range cmd.Commands() {
			if sub.IsAvailableCommand() && len(sub.Name()) > maxLen {
				maxLen = len(sub.Name())
			}
		}

		for _, sub := range cmd.Commands() {
			if !sub.IsAvailableCommand() {
				continue
			}
			name := helpCommand.Render(sub.Name())
			padding := strings.Repeat(" ", maxLen-len(sub.Name())+2)
			b.WriteString(fmt.Sprintf("  %s%s%s\n", name, padding, sub.Short))
		}
		b.WriteString("\n")
	}

	// Local flags.
	if cmd.HasAvailableLocalFlags() {
		b.WriteString(helpHeading.Render("Flags"))
		b.WriteString("\n")
		b.WriteString(styleFlags(cmd.LocalFlags().FlagUsages()))
		b.WriteString("\n")
	}

	// Inherited flags.
	if cmd.HasAvailableInheritedFlags() {
		b.WriteString(helpHeading.Render("Global Flags"))
		b.WriteString("\n")
		b.WriteString(styleFlags(cmd.InheritedFlags().FlagUsages()))
		b.WriteString("\n")
	}

	// Footer.
	if cmd.HasAvailableSubCommands() {
		footer := fmt.Sprintf(`Use "%s [command] --help" for more information about a command.`, cmd.CommandPath())
		b.WriteString(helpDim.Render(footer))
		b.WriteString("\n")
	}

	fmt.Fprint(cmd.OutOrStdout(), b.String())
}

// styleFlags colorizes flag names in Cobra's flag usage output.
func styleFlags(usage string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(usage, "\n"), "\n") {
		b.WriteString(styleFlagLine(line))
		b.WriteString("\n")
	}
	return b.String()
}

// styleFlagLine applies color to a single flag usage line.
// Cobra outputs lines like: "  -f, --format string   description text"
func styleFlagLine(line string) string {
	if line == "" {
		return line
	}

	trimmed := strings.TrimLeft(line, " ")
	indent := strings.Repeat(" ", len(line)-len(trimmed))

	// Split on the double-space boundary between flags and description.
	parts := strings.SplitN(trimmed, "   ", 2)
	if len(parts) == 1 {
		return indent + helpFlag.Render(trimmed)
	}

	flagPart := parts[0]
	descPart := strings.TrimLeft(parts[1], " ")

	totalFlagWidth := len(flagPart)
	originalSpacing := len(trimmed) - len(flagPart) - len(strings.TrimLeft(parts[1], " "))
	spacing := strings.Repeat(" ", originalSpacing+totalFlagWidth-len(flagPart))

	return indent + helpFlag.Render(flagPart) + spacing + descPart
}
