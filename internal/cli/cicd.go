package cli

import (
	"context"
	"fmt"

	gh "github.com/nickawilliams/bosun/internal/code/github"
)

func init() { registerSource("github_actions", "repository", workflowRepositorySource) }

// workflowRepositorySource returns the project's managed repositories as
// SourceOptions in owner/repo format for the config picker.
func workflowRepositorySource() ([]SourceOption, error) {
	repositories, err := resolveRepositories(nil)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	var opts []SourceOption
	for _, r := range repositories {
		identity, err := gh.ParseRemote(ctx, r.Path)
		if err != nil {
			continue
		}
		value := fmt.Sprintf("%s/%s", identity.Owner, identity.Name)
		opts = append(opts, SourceOption{
			Label: fmt.Sprintf("%s  (%s)", r.Name, value),
			Value: value,
		})
	}
	return opts, nil
}
