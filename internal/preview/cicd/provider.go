package cicd

import (
	"context"

	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/provider"
)

// providerName is the value that selects this adapter in
// preview.provider.
const providerName = "cicd"

// Descriptor registers the workflow-dispatch adapter with the services
// registry.
//
// It declares no config keys of its own. Everything it reads —
// preview.up.workflow, the input-name mappings, the URL template — is
// resolved by the CLI into preview.Deps, because resolving a target
// means intersecting workflow config with the active workspace's
// repositories, which is the CLI's knowledge rather than an adapter's.
//
// Default is set because this is the adapter that shipped first: every
// config written before the HTTP adapter existed omits
// preview.provider, and those configs must keep working.
func Descriptor() preview.ProviderDescriptor {
	return preview.ProviderDescriptor{
		Name:    providerName,
		Default: true,
		New: func(_ provider.Config, deps preview.Deps) (preview.Provider, error) {
			return New(Options{
				Pipeline:    deps.Workflow.Pipeline,
				Tracker:     deps.Tracker,
				Stage:       deps.Workflow.Stage,
				URLTemplate: deps.URLTemplate,
				Targets:     workflowTargets(deps),
				InputName:   inputNames(deps),
			}), nil
		},
	}
}

// workflowTargets adapts the Deps-shaped target resolver to the
// adapter's own Target type. The two types are the same fields either
// side of the CLI-to-adapter seam: Deps carries the resolver across it,
// and Options is the adapter's own tested constructor surface, so
// neither collapses into the other.
//
// A nil resolver yields no targets rather than a panic — Create and
// Destroy then report ErrNoWorkflow, which is the same answer a
// configured-but-empty stage gives.
func workflowTargets(deps preview.Deps) func(context.Context, string) ([]Target, error) {
	return func(ctx context.Context, subStage string) ([]Target, error) {
		if deps.Workflow.Targets == nil {
			return nil, nil
		}
		raw, err := deps.Workflow.Targets(ctx, subStage)
		if err != nil {
			return nil, err
		}
		out := make([]Target, len(raw))
		for i, t := range raw {
			out[i] = Target{Owner: t.Owner, Repo: t.Repo, Workflow: t.Workflow, Label: t.Label}
		}
		return out, nil
	}
}

// inputNames supplies the input-name lookup, standing in an
// always-unconfigured one when Deps carries none. Options documents
// InputName as non-nil and Destroy dereferences it directly; a nil here
// would turn a partially-wired Deps into a panic instead of the
// "workflow has no name input configured" refusal that path already has.
func inputNames(deps preview.Deps) func(subStage, concept string) string {
	if deps.Workflow.InputName == nil {
		return func(string, string) string { return "" }
	}
	return deps.Workflow.InputName
}
