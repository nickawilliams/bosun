package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// isInteractive returns true if stdin is a terminal.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptValue displays a prompt and returns the user's input, or the default
// value if the user enters nothing. Used by init and other interactive flows.
func promptValue(scanner *bufio.Scanner, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}

	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			return input
		}
	}
	return defaultVal
}

// promptRequired prompts for a value if stdin is a terminal. If stdin is not
// a terminal (piped/scripted), returns empty string and the caller should
// error. label is the user-facing prompt text.
func promptRequired(label string) string {
	if !isInteractive() {
		return ""
	}

	fmt.Printf("%s: ", label)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}
