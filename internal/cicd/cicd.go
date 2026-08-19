package cicd

import (
	"context"

	"github.com/nickawilliams/bosun/internal/provider"
)

// ConfigGroup is the config group that holds CI/CD settings.
const ConfigGroup = "cicd"

// TriggerRequest holds the parameters for triggering a CI/CD workflow.
type TriggerRequest struct {
	Owner      string            // Repository owner (e.g., GitHub org or user).
	Repository string            // Repository name.
	Workflow   string            // Workflow filename (e.g., "deploy-preview.yml").
	Ref        string            // Git ref to run against (branch, tag, SHA).
	Inputs     map[string]string // Workflow dispatch inputs.
}

// CICD abstracts CI/CD pipeline operations.
type CICD interface {
	// TriggerWorkflow dispatches a workflow run.
	TriggerWorkflow(ctx context.Context, req TriggerRequest) error

	// AuthTest verifies the pipeline's credentials and returns a display
	// string identifying what it authenticated as (e.g.
	// "github actions → nickawilliams"). The whole string is the
	// provider's: which credentials a pipeline uses is its own business
	// (GitHub Actions borrows the code host's token), and so is the name
	// it goes by in a doctor row.
	AuthTest(ctx context.Context) (string, error)
}

// Descriptor is what a CI/CD provider package contributes to bosun: the
// config value that selects it, the configuration it needs, and how to
// build it. See issue.TrackerDescriptor for the shape's rationale.
type Descriptor struct {
	// Name is the value that selects this provider in config
	// (cicd.provider), e.g. "github_actions".
	Name string

	// Keys are the provider-specific config keys under the "cicd" group,
	// relative to it. Empty for providers whose credentials live in
	// another group entirely — GitHub Actions authenticates with the
	// code host's token and contributes no keys of its own.
	Keys []provider.ConfigKey

	// New constructs the pipeline from configuration.
	New func(provider.Config) (CICD, error)
}
