//go:build !darwin && !linux

package slack

import "fmt"

const pbkdf2Iterations = 1

func slackLevelDBPath() (string, error) {
	return "", fmt.Errorf("local Slack token resolution is not supported on this platform")
}

func slackCookiesPath() (string, error) {
	return "", fmt.Errorf("local Slack cookie resolution is not supported on this platform")
}

func decryptionKey() ([]byte, error) {
	return nil, fmt.Errorf("local Slack decryption is not supported on this platform")
}
