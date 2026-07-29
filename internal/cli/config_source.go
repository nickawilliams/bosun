package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/viper"
)

// Config source tier names.
const (
	sourceDefault = "default"
	sourceGlobal  = "global"
	sourceProject = "project"
	sourceEnv     = "env"
)

// configSources holds pre-loaded viper instances for each config
// tier, allowing source attribution without re-reading files per key.
type configSources struct {
	global      *viper.Viper
	project     *viper.Viper
	globalPath  string
	projectPath string
}

// loadConfigSources reads the global and project config files
// independently into separate viper instances.
func loadConfigSources() *configSources {
	cs := &configSources{}

	if dir, err := config.GlobalConfigDir(); err == nil {
		cs.globalPath = filepath.Join(dir, "config.yaml")
		cs.global = viper.New()
		cs.global.SetConfigFile(cs.globalPath)
		_ = cs.global.ReadInConfig()
	}

	if root := config.FindProjectRoot(); root != "" {
		cs.projectPath = filepath.Join(root, ".bosun", "config.yaml")
		cs.project = viper.New()
		cs.project.SetConfigFile(cs.projectPath)
		_ = cs.project.ReadInConfig()
	}

	return cs
}

// resolveSource determines where a schema config key's effective
// value comes from. Returns the value and its source tier.
func (cs *configSources) resolveSource(groupName string, ck ConfigKey) (value, source string) {
	fk := fullKey(groupName, ck)

	if v := effectiveEnvValue(groupName, ck); v != "" {
		return v, sourceEnv
	}

	// Project config.
	if cs.project != nil && cs.project.IsSet(fk) {
		return fmt.Sprintf("%v", cs.project.Get(fk)), sourceProject
	}

	// Global config.
	if cs.global != nil && cs.global.IsSet(fk) {
		return fmt.Sprintf("%v", cs.global.Get(fk)), sourceGlobal
	}

	// Schema default.
	if ck.Default != "" {
		return ck.Default, sourceDefault
	}

	return "", ""
}

// effectiveEnvValue returns the value provided via the schema's
// explicit EnvVar (e.g., GITHUB_TOKEN) or the automatic BOSUN_*
// computed name for the key, whichever resolves first. Returns ""
// when neither is set. Shared by resolveSource (source attribution)
// and resolveConfigValue (validation) so both honor the same env-var
// resolution order — fixing the longstanding gap where viper's
// AutomaticEnv missed dotted keys (no SetEnvKeyReplacer for `.`→`_`)
// and the schema's explicit EnvVar was never consulted at all.
func effectiveEnvValue(groupName string, ck ConfigKey) string {
	if ck.EnvVar != "" {
		if v := os.Getenv(ck.EnvVar); v != "" {
			return v
		}
	}
	return os.Getenv(envVarForKey(fullKey(groupName, ck)))
}

// resolveConfigValue returns the effective string value for a schema
// key without the source label — env (explicit or automatic) →
// merged viper (project + global) → schema default. Use this in
// validation paths so env-provided values aren't reported as
// "missing".
func resolveConfigValue(groupName string, ck ConfigKey) string {
	if v := effectiveEnvValue(groupName, ck); v != "" {
		return v
	}
	if v := viper.GetString(fullKey(groupName, ck)); v != "" {
		return v
	}
	return ck.Default
}

// resolveKeySource determines where an arbitrary viper key's
// effective value comes from (no schema metadata available).
func (cs *configSources) resolveKeySource(key string) (value, source string) {
	// 1. Automatic BOSUN_* env var.
	if v := os.Getenv(envVarForKey(key)); v != "" {
		return v, sourceEnv
	}

	// 2. Project config.
	if cs.project != nil && cs.project.IsSet(key) {
		return fmt.Sprintf("%v", cs.project.Get(key)), sourceProject
	}

	// 3. Global config.
	if cs.global != nil && cs.global.IsSet(key) {
		return fmt.Sprintf("%v", cs.global.Get(key)), sourceGlobal
	}

	return "", ""
}

// envVarForKey returns the automatic BOSUN_* env var name for a
// viper key (e.g., "issue_tracker.base_url" → "BOSUN_ISSUE_TRACKER_BASE_URL").
func envVarForKey(key string) string {
	return "BOSUN_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// flattenMap recursively flattens a nested map into dot-separated keys.
func flattenMap(prefix string, m map[string]any) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			for fk, fv := range flattenMap(key, val) {
				result[fk] = fv
			}
		default:
			result[key] = fmt.Sprintf("%v", val)
		}
	}
	return result
}
