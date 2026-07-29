package cli

import (
	"testing"

	"github.com/spf13/viper"
)

// TestEnsureConfigValue locks the env-var awareness of the JIT config
// gate: values supplied via the key's explicit EnvVar or the automatic
// BOSUN_* name count as configured AND are materialized into viper so
// the provider factories' bare viper reads agree with the gate
// (regression: config check honored env vars while requireConfig
// re-prompted for the same token).
func TestEnsureConfigValue(t *testing.T) {
	t.Run("explicit EnvVar materializes into viper", func(t *testing.T) {
		ck := ConfigKey{Key: "token", EnvVar: "BOSUN_TEST_ECV_EXPLICIT"}
		t.Setenv("BOSUN_TEST_ECV_EXPLICIT", "sekrit")

		if !ensureConfigValue("testgroup_ecv1", ck) {
			t.Fatal("ensureConfigValue() = false, want true for explicit EnvVar")
		}
		if got := viper.GetString("testgroup_ecv1.token"); got != "sekrit" {
			t.Errorf("viper value = %q, want the env value materialized", got)
		}
	})

	t.Run("automatic BOSUN_* var materializes into viper", func(t *testing.T) {
		ck := ConfigKey{Key: "base_url"}
		t.Setenv("BOSUN_TESTGROUP_ECV2_BASE_URL", "https://x.example")

		if !ensureConfigValue("testgroup_ecv2", ck) {
			t.Fatal("ensureConfigValue() = false, want true for automatic env var")
		}
		if got := viper.GetString("testgroup_ecv2.base_url"); got != "https://x.example" {
			t.Errorf("viper value = %q, want the env value materialized", got)
		}
	})

	t.Run("absent everywhere is not configured", func(t *testing.T) {
		ck := ConfigKey{Key: "token", EnvVar: "BOSUN_TEST_ECV_ABSENT"}
		if ensureConfigValue("testgroup_ecv3", ck) {
			t.Error("ensureConfigValue() = true for a value set nowhere")
		}
	})
}
