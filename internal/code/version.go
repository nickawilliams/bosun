package code

import (
	"fmt"
	"strconv"
	"strings"
)

// DeriveNextVersion parses a semver tag and increments based on bump level.
//
//	"v1.2.3", "patch" → "v1.2.4"
//	"v1.2.3", "minor" → "v1.3.0"
//	"v1.2.3", "major" → "v2.0.0"
//	"",       "patch" → "v0.0.1"
func DeriveNextVersion(current, bump string) (string, error) {
	major, minor, patch := 0, 0, 0

	if current != "" {
		v := strings.TrimPrefix(current, "v")
		parts := strings.SplitN(v, ".", 3)
		if len(parts) != 3 {
			return "", fmt.Errorf("invalid semver: %q", current)
		}
		var err error
		major, err = strconv.Atoi(parts[0])
		if err != nil {
			return "", fmt.Errorf("invalid major version in %q: %w", current, err)
		}
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", fmt.Errorf("invalid minor version in %q: %w", current, err)
		}
		// Patch may have pre-release suffix (e.g., "3-beta.1"); strip it.
		patchStr := strings.SplitN(parts[2], "-", 2)[0]
		patch, err = strconv.Atoi(patchStr)
		if err != nil {
			return "", fmt.Errorf("invalid patch version in %q: %w", current, err)
		}
	}

	switch bump {
	case "major":
		major++
		minor = 0
		patch = 0
	case "minor":
		minor++
		patch = 0
	case "patch", "":
		patch++
	default:
		return "", fmt.Errorf("invalid bump level: %q (use patch, minor, or major)", bump)
	}

	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}
