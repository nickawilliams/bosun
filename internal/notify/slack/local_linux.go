package slack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const pbkdf2Iterations = 1

func slackLevelDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}

	p := filepath.Join(home, ".config/Slack/Local Storage/leveldb")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("Slack LevelDB not found at %s", p)
	}

	return p, nil
}

func slackCookiesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}

	p := filepath.Join(home, ".config/Slack/Cookies")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("Slack Cookies database not found at %s", p)
	}

	return p, nil
}

func decryptionKey() ([]byte, error) {
	// Try GNOME Keyring / Secret Service via secret-tool.
	out, err := exec.Command("secret-tool", "lookup",
		"application", "slack").Output()
	if err == nil {
		key := strings.TrimSpace(string(out))
		if key != "" {
			return []byte(key), nil
		}
	}

	// Fallback: Chromium uses "peanuts" when no keyring is available.
	return []byte("peanuts"), nil
}
