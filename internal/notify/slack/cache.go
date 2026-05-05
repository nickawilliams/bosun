package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/notify"
)

const (
	cacheTTL  = 5 * time.Minute
	cacheFile = "cache/slack.json"
)

// cacheEntry is a single cached value with an expiration timestamp.
type cacheEntry[T any] struct {
	Value   T         `json:"value"`
	Expires time.Time `json:"expires"`
}

// persistentCache is the on-disk representation of the Slack API cache.
type persistentCache struct {
	Channels map[string]cacheEntry[string]          `json:"channels,omitempty"`
	Threads  map[string]cacheEntry[notify.ThreadRef] `json:"threads,omitempty"`
}

// cachePath returns the full path to the cache file.
func cachePath() string {
	dir, err := config.GlobalConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, cacheFile)
}

// loadCache reads the persistent cache from disk into the adapter's apiCache.
// Expired entries are pruned. Returns an empty cache on any error.
func loadCache() apiCache {
	c := apiCache{
		channels: make(map[string]string),
		threads:  make(map[string]notify.ThreadRef),
	}

	path := cachePath()
	if path == "" {
		return c
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}

	var pc persistentCache
	if err := json.Unmarshal(data, &pc); err != nil {
		return c
	}

	now := time.Now()

	for k, v := range pc.Channels {
		if now.Before(v.Expires) {
			c.channels[k] = v.Value
		}
	}
	for k, v := range pc.Threads {
		if now.Before(v.Expires) {
			c.threads[k] = v.Value
		}
	}

	return c
}

// saveCache writes the adapter's apiCache to disk. Existing entries from the
// file that are still valid are preserved (merge, not overwrite).
func saveCache(c apiCache) {
	path := cachePath()
	if path == "" {
		return
	}

	// Load existing to merge.
	existing := persistentCache{
		Channels: make(map[string]cacheEntry[string]),
		Threads:  make(map[string]cacheEntry[notify.ThreadRef]),
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	now := time.Now()
	expires := now.Add(cacheTTL)

	// Prune expired entries from existing.
	for k, v := range existing.Channels {
		if now.After(v.Expires) {
			delete(existing.Channels, k)
		}
	}
	for k, v := range existing.Threads {
		if now.After(v.Expires) {
			delete(existing.Threads, k)
		}
	}

	// Merge in current session's entries.
	for k, v := range c.channels {
		existing.Channels[k] = cacheEntry[string]{Value: v, Expires: expires}
	}
	for k, v := range c.threads {
		existing.Threads[k] = cacheEntry[notify.ThreadRef]{Value: v, Expires: expires}
	}

	data, err := json.Marshal(existing)
	if err != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}
