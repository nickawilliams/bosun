package cli

import "sort"

// releaseTagsInSemverOrder filters tags down to release-shaped ones
// (releaseTagPattern) and returns them sorted ascending by semver. The
// lowest entry is the release that first shipped a probed commit — the
// canonical "which release contains this work" answer. Pure.
func releaseTagsInSemverOrder(tags []string) []string {
	ordered := make([]string, 0, len(tags))
	for _, t := range tags {
		if releaseTagPattern.MatchString(t) {
			ordered = append(ordered, t)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return compareSemverTag(ordered[i], ordered[j]) < 0
	})
	return ordered
}

// lowestContainingReleaseTag returns the lowest-semver release-shaped tag
// from tags (the release that first shipped a probed commit), or "" when
// none match. Callers pass the output of vcs.TagsContaining(sha) after a
// tag fetch. Pure.
func lowestContainingReleaseTag(tags []string) string {
	ordered := releaseTagsInSemverOrder(tags)
	if len(ordered) == 0 {
		return ""
	}
	return ordered[0]
}
