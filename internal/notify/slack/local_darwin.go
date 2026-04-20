package slack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const pbkdf2Iterations = 1003

func slackLevelDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}

	paths := []string{
		filepath.Join(home, "Library/Application Support/Slack/Local Storage/leveldb"),
		filepath.Join(home, "Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Local Storage/leveldb"),
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("Slack LevelDB not found (checked %s)", strings.Join(paths, ", "))
}

func slackCookiesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}

	paths := []string{
		filepath.Join(home, "Library/Application Support/Slack/Cookies"),
		filepath.Join(home, "Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Cookies"),
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("Slack Cookies database not found (checked %s)", strings.Join(paths, ", "))
}

func decryptionKey() ([]byte, error) {
	// The encryption key is stored in the macOS Keychain under
	// "Slack Safe Storage" (or "Chrome Safe Storage" for older versions).
	for _, service := range []string{"Slack Safe Storage", "Chrome Safe Storage"} {
		out, err := exec.Command("security", "find-generic-password",
			"-s", service, "-w").Output()
		if err == nil {
			return []byte(strings.TrimSpace(string(out))), nil
		}
	}

	return nil, fmt.Errorf("could not read Slack encryption key from Keychain (grant access when prompted)")
}
