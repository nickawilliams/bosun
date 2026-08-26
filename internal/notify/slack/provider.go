package slack

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/provider"
)

// providerName is the value that selects this adapter in
// notification.provider.
const providerName = "slack"

// Descriptor registers the Slack adapter with the services registry:
// the config it needs, and how to build it from that config.
//
// The channel keys (notification.channels.*) are deliberately
// absent: routing a notification to a named channel is something any
// chat provider does, so those stay in bosun's schema. What's here is
// what only Slack has — its two auth modes and the workspace name the
// local-token path needs.
func Descriptor() notify.NotifierDescriptor {
	return notify.NotifierDescriptor{
		Name: providerName,
		Keys: []provider.ConfigKey{
			{Key: "auth", Label: "auth method", Options: []string{"token", "local"}, Default: "token"},
			{Key: "token", Label: "API token", EnvVar: "BOSUN_SLACK_TOKEN", Secret: true},
			{Key: "workspace", Label: "workspace name", Example: "mycompany"},
		},
		New: func(cfg provider.Config) (notify.Notifier, error) {
			if cfg.Get(notify.ConfigGroup+".auth") == "local" {
				workspace := cfg.Get(notify.ConfigGroup + ".workspace")
				if workspace == "" {
					return nil, fmt.Errorf("%s.workspace required for local auth", notify.ConfigGroup)
				}
				token, cookie, err := ResolveLocalToken(workspace)
				if err != nil {
					return nil, fmt.Errorf("resolving local Slack token: %w", err)
				}
				return NewWithCookie(token, cookie), nil
			}

			// Token-based auth.
			if err := cfg.Require(notify.ConfigGroup); err != nil {
				return nil, err
			}
			return New(cfg.Get(notify.ConfigGroup + ".token")), nil
		},
	}
}
