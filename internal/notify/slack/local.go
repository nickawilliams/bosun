package slack

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"golang.org/x/crypto/pbkdf2"

	_ "modernc.org/sqlite"
)

// ResolveLocalToken extracts the xoxc- token and d cookie from the Slack
// desktop app's local storage. Requires Slack to be installed and the user
// to be logged in. The workspace parameter should match the workspace name
// as shown in the Slack app (e.g., "mycompany").
func ResolveLocalToken(workspace string) (token, cookie string, err error) {
	dbPath, err := slackLevelDBPath()
	if err != nil {
		return "", "", err
	}

	token, err = readToken(dbPath, workspace)
	if err != nil {
		return "", "", fmt.Errorf("reading token: %w", err)
	}

	cookiesPath, err := slackCookiesPath()
	if err != nil {
		return "", "", err
	}

	key, err := decryptionKey()
	if err != nil {
		return "", "", fmt.Errorf("getting decryption key: %w", err)
	}

	cookie, err = readCookie(cookiesPath, key)
	if err != nil {
		return "", "", fmt.Errorf("reading cookie: %w", err)
	}

	return token, cookie, nil
}

// readToken opens the LevelDB (via temp copy to avoid lock conflicts) and
// extracts the xoxc- token for the given workspace.
func readToken(dbPath, workspace string) (string, error) {
	tmpDir, cleanup, err := copyLevelDB(dbPath)
	if err != nil {
		return "", err
	}
	defer cleanup()

	db, err := leveldb.OpenFile(tmpDir, &opt.Options{ReadOnly: true})
	if err != nil {
		return "", fmt.Errorf("opening LevelDB: %w", err)
	}
	defer db.Close()

	iter := db.NewIterator(nil, nil)
	defer iter.Release()

	for iter.Next() {
		key := string(iter.Key())
		if !strings.Contains(key, "localConfig_v2") {
			continue
		}

		value := iter.Value()
		config, err := parseLocalConfig(value)
		if err != nil {
			return "", fmt.Errorf("parsing localConfig_v2: %w", err)
		}

		return extractToken(config, workspace)
	}

	if err := iter.Error(); err != nil {
		return "", fmt.Errorf("iterating LevelDB: %w", err)
	}

	return "", fmt.Errorf("localConfig_v2 not found in LevelDB")
}

// parseLocalConfig handles the various encodings Chromium/Electron may use
// for the localConfig_v2 value (UTF-8, UTF-16LE, with possible prefix bytes).
func parseLocalConfig(raw []byte) (map[string]any, error) {
	// Strip potential Chromium prefix byte (0x00, 0x01, 0x02).
	if len(raw) > 0 && raw[0] <= 0x02 {
		raw = raw[1:]
	}

	// Try UTF-8 first.
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err == nil {
		return config, nil
	}

	// Try UTF-16LE decoding.
	decoded := decodeUTF16LE(raw)
	if err := json.Unmarshal([]byte(decoded), &config); err == nil {
		return config, nil
	}

	return nil, fmt.Errorf("could not parse localConfig_v2 as JSON (len=%d)", len(raw))
}

// decodeUTF16LE converts a UTF-16LE byte slice to a UTF-8 string.
func decodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u16))
}

// extractToken finds the token for the named workspace in the parsed config.
func extractToken(config map[string]any, workspace string) (string, error) {
	teams, ok := config["teams"]
	if !ok {
		return "", fmt.Errorf("no 'teams' key in localConfig_v2")
	}

	teamsMap, ok := teams.(map[string]any)
	if !ok {
		return "", fmt.Errorf("'teams' is not an object")
	}

	workspace = strings.ToLower(workspace)
	var available []string

	for _, team := range teamsMap {
		teamMap, ok := team.(map[string]any)
		if !ok {
			continue
		}

		name, _ := teamMap["name"].(string)
		available = append(available, name)

		if strings.ToLower(name) == workspace {
			token, _ := teamMap["token"].(string)
			if token == "" {
				return "", fmt.Errorf("workspace %q found but token is empty", workspace)
			}
			return token, nil
		}
	}

	return "", fmt.Errorf("workspace %q not found (available: %s)", workspace, strings.Join(available, ", "))
}

// readCookie reads and decrypts the Slack d cookie from the Cookies SQLite database.
func readCookie(cookiesPath string, key []byte) (string, error) {
	db, err := sql.Open("sqlite", cookiesPath+"?mode=ro")
	if err != nil {
		return "", fmt.Errorf("opening Cookies database: %w", err)
	}
	defer db.Close()

	var encrypted []byte
	err = db.QueryRow(
		`SELECT encrypted_value FROM cookies WHERE host_key = '.slack.com' AND name = 'd' LIMIT 1`,
	).Scan(&encrypted)
	if err != nil {
		return "", fmt.Errorf("querying d cookie: %w", err)
	}

	if len(encrypted) == 0 {
		return "", fmt.Errorf("d cookie is empty")
	}

	return decryptCookie(encrypted, key)
}

// decryptCookie decrypts a Chromium-encrypted cookie value.
func decryptCookie(encrypted, password []byte) (string, error) {
	// Strip "v10" or "v11" prefix (3 bytes).
	if len(encrypted) < 4 {
		return "", fmt.Errorf("encrypted value too short (%d bytes)", len(encrypted))
	}
	if encrypted[0] != 'v' || encrypted[1] != '1' {
		return "", fmt.Errorf("unexpected encryption version: %q", encrypted[:3])
	}
	encrypted = encrypted[3:]

	// Derive key using PBKDF2.
	derivedKey := pbkdf2.Key(password, []byte("saltysalt"), pbkdf2Iterations, 16, sha1.New)

	// Decrypt using AES-128-CBC with IV = 16 space bytes.
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", fmt.Errorf("creating cipher: %w", err)
	}

	iv := []byte("                ") // 16 space bytes (0x20)

	if len(encrypted)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ciphertext length %d is not a multiple of block size", len(encrypted))
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(encrypted))
	mode.CryptBlocks(plaintext, encrypted)

	// Remove PKCS#5 padding.
	plaintext, err = removePKCS5Padding(plaintext)
	if err != nil {
		return "", fmt.Errorf("removing padding: %w", err)
	}

	return string(plaintext), nil
}

// removePKCS5Padding removes PKCS#5/PKCS#7 padding from decrypted data.
func removePKCS5Padding(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid padding value: %d", padding)
	}

	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding at position %d", i)
		}
	}

	return data[:len(data)-padding], nil
}

// copyLevelDB copies the LevelDB directory to a temp location so it can be
// read without conflicting with Slack's exclusive lock.
func copyLevelDB(src string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "bosun-leveldb-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp directory: %w", err)
	}

	cleanup := func() { os.RemoveAll(tmpDir) }

	entries, err := os.ReadDir(src)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("reading LevelDB directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		srcFile := filepath.Join(src, entry.Name())
		dstFile := filepath.Join(tmpDir, entry.Name())

		if err := copyFile(srcFile, dstFile); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("copying %s: %w", entry.Name(), err)
		}
	}

	return tmpDir, cleanup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
