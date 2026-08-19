package cli

import (
	"github.com/nickawilliams/bosun/internal/provider"
	"github.com/spf13/viper"
)

// providerConfig adapts bosun's configuration to the narrow read side a
// provider adapter sees: viper for values, requireConfig for the
// just-in-time completion that prompts for what's missing and persists
// it. Adapters get keys and strings; the terminal, the schema, and the
// config files stay on this side of the boundary.
type providerConfig struct{}

func (providerConfig) Get(key string) string { return viper.GetString(key) }

func (providerConfig) Require(keys ...string) error { return requireConfig(keys...) }

// Verify providerConfig satisfies provider.Config at compile time.
var _ provider.Config = providerConfig{}
