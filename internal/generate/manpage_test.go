package generate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestWriteManPage_Comprehensive(t *testing.T) {
	root := &cobra.Command{
		Use:   "deploy",
		Short: "A deployment tool for managing application releases",
		Long:  "deploy manages application releases across environments.",
	}
	root.PersistentFlags().BoolP("verbose", "v", false, "enable verbose output")
	root.Flags().StringP("config", "c", "", "config file path")

	push := &cobra.Command{Use: "push", Short: "Deploy the application"}
	migrate := &cobra.Command{Use: "migrate", Short: "Apply pending migrations"}
	hidden := &cobra.Command{Use: "secret", Short: "Hidden", Hidden: true}
	root.AddCommand(push, migrate, hidden)

	var buf bytes.Buffer
	if err := WriteManPage(&buf, root, ManPageOptions{Date: "Jan 2026"}); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	checks := []struct {
		label string
		text  string
	}{
		{"TH header", `.TH "DEPLOY" "1" "Jan 2026" "deploy" "User Commands"`},
		{"NAME section", ".SH NAME"},
		{"name with brief", `deploy \- A deployment tool`},
		{"SYNOPSIS section", ".SH SYNOPSIS"},
		{"DESCRIPTION section", ".SH DESCRIPTION"},
		{"OPTIONS section", ".SH OPTIONS"},
		{"verbose flag", `\fB\-v\fP, \fB\-\-verbose\fP`},
		{"config option", `\fB\-c\fP, \fB\-\-config\fP=\fICONFIG\fP`},
		{"help flag", `\fB\-h\fP, \fB\-\-help\fP`},
		{"COMMANDS section", ".SH COMMANDS"},
		{"push subcommand", `\fBpush\fP`},
		{"migrate subcommand", `\fBmigrate\fP`},
		{"EXIT STATUS section", ".SH EXIT STATUS"},
		{"SEE ALSO section", ".SH SEE ALSO"},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.text) {
			t.Errorf("[%s] missing %q\n\nfull output:\n%s", c.label, c.text, got)
		}
	}
	if strings.Contains(got, "secret") {
		t.Errorf("hidden subcommand leaked into output:\n%s", got)
	}
}

func TestWriteManPage_NoLongFallsBackToShort(t *testing.T) {
	root := &cobra.Command{Use: "tool", Short: "A small tool"}
	var buf bytes.Buffer
	if err := WriteManPage(&buf, root, ManPageOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `A small tool`) {
		t.Errorf("DESCRIPTION should fall back to Short when Long is empty:\n%s", buf.String())
	}
}

func TestWriteManPage_NoSubcommandsHidesCommandsSection(t *testing.T) {
	root := &cobra.Command{Use: "leaf", Short: "Leaf command"}
	var buf bytes.Buffer
	if err := WriteManPage(&buf, root, ManPageOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), ".SH COMMANDS") {
		t.Errorf("COMMANDS section should be hidden when no visible subcommands")
	}
}

func TestTroffEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"--verbose", `\-\-verbose`},
		{"-v", `\-v`},
		{"plain text", "plain text"},
		{`back\slash`, `back\\slash`},
	}
	for _, tt := range tests {
		if got := troffEscape(tt.input); got != tt.want {
			t.Errorf("troffEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
