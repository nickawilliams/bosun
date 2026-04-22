package cicd

import "context"

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
}
