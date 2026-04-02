package sources

import (
	"fmt"
	"net/url"
	"regexp"
)

// maxResponseBytes caps upstream HTTP response reads to guard against unexpectedly large payloads.
const maxResponseBytes = 4 * 1024 * 1024 // 4 MiB

var (
	repoNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,100}$`)
	slugRe     = regexp.MustCompile(`^[a-zA-Z0-9-]{1,100}$`)
)

// safeURL joins a base URL with path segments using url.JoinPath, which
// percent-encodes each segment and cleans any traversal sequences.
func safeURL(base string, segments ...string) (string, error) {
	u, err := url.JoinPath(base, segments...)
	if err != nil {
		return "", fmt.Errorf("building URL: %w", err)
	}
	return u, nil
}

func validateRepoName(name string) error {
	if !repoNameRe.MatchString(name) {
		return fmt.Errorf("invalid repo name %q", name)
	}
	return nil
}

func validateSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("invalid slug %q", slug)
	}
	return nil
}
