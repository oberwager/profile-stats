package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)


type GitHubClient struct {
	pat        string
	owner      string
	httpClient *http.Client
}

func NewGitHubClient(pat, owner string) *GitHubClient {
	return &GitHubClient{
		pat:   pat,
		owner: owner,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type WorkflowRun struct {
	Conclusion string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Path       string
}

type workflowRunsResponse struct {
	WorkflowRuns []struct {
		Conclusion string    `json:"conclusion"`
		Status     string    `json:"status"`
		CreatedAt  time.Time `json:"created_at"`
		UpdatedAt  time.Time `json:"updated_at"`
		Path       string    `json:"path"`
	} `json:"workflow_runs"`
}

// WorkflowRuns fetches the last 20 push-triggered docker-publish.yml runs for a repo.
func (c *GitHubClient) WorkflowRuns(ctx context.Context, repo string) ([]WorkflowRun, error) {
	if err := validateRepoName(repo); err != nil {
		return nil, fmt.Errorf("github workflow runs: %w", err)
	}
	base, err := safeURL("https://api.github.com", "repos", c.owner, repo, "actions/runs")
	if err != nil {
		return nil, fmt.Errorf("github workflow runs URL: %w", err)
	}
	u := base + "?per_page=20&event=push"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("github workflow runs request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github workflow runs fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("github workflow runs read: %w", err)
	}

	var raw workflowRunsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("github workflow runs parse: %w", err)
	}

	var runs []WorkflowRun
	for _, r := range raw.WorkflowRuns {
		if r.Path != ".github/workflows/docker-publish.yml" {
			continue
		}
		runs = append(runs, WorkflowRun{
			Conclusion: r.Conclusion,
			Status:     r.Status,
			CreatedAt:  r.CreatedAt,
			UpdatedAt:  r.UpdatedAt,
			Path:       r.Path,
		})
	}

	return runs, nil
}

// ImageManifest fetches the OCI manifest for the :latest tag of a ghcr.io image.
func (c *GitHubClient) ImageManifest(ctx context.Context, repo string) (digest string, sizeBytes int64, err error) {
	if err := validateRepoName(repo); err != nil {
		return "", 0, fmt.Errorf("ghcr manifest: %w", err)
	}
	u, err := safeURL("https://ghcr.io", "v2", c.owner, repo, "manifests/latest")
	if err != nil {
		return "", 0, fmt.Errorf("ghcr manifest URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, fmt.Errorf("ghcr manifest request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("ghcr manifest fetch: %w", err)
	}
	defer resp.Body.Close()

	digest = resp.Header.Get("Docker-Content-Digest")

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return digest, 0, fmt.Errorf("ghcr manifest read: %w", err)
	}

	var manifest struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        struct {
			Size int64 `json:"size"`
		} `json:"config"`
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return digest, 0, fmt.Errorf("ghcr manifest parse: %w", err)
	}

	var total int64
	total += manifest.Config.Size
	for _, layer := range manifest.Layers {
		total += layer.Size
	}

	return digest, total, nil
}

var semverRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// CurrentVersion fetches the tag list and returns the semver tag matching the :latest digest.
func (c *GitHubClient) CurrentVersion(ctx context.Context, repo, latestDigest string) (string, error) {
	if err := validateRepoName(repo); err != nil {
		return "", fmt.Errorf("ghcr tags: %w", err)
	}
	tagsURL, err := safeURL("https://ghcr.io", "v2", c.owner, repo, "tags/list")
	if err != nil {
		return "", fmt.Errorf("ghcr tags URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return "", fmt.Errorf("ghcr tags request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ghcr tags fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("ghcr tags read: %w", err)
	}

	var tagList struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(body, &tagList); err != nil {
		return "", fmt.Errorf("ghcr tags parse: %w", err)
	}

	for _, tag := range tagList.Tags {
		if !semverRe.MatchString(tag) {
			continue
		}
		tagDigest, _, err := c.fetchManifestDigest(ctx, repo, tag)
		if err != nil {
			continue
		}
		if tagDigest == latestDigest {
			return tag, nil
		}
	}

	return "", nil
}

func (c *GitHubClient) fetchManifestDigest(ctx context.Context, repo, tag string) (string, int64, error) {
	u, err := safeURL("https://ghcr.io", "v2", c.owner, repo, "manifests", tag)
	if err != nil {
		return "", 0, fmt.Errorf("ghcr manifest digest URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return resp.Header.Get("Docker-Content-Digest"), 0, nil
}

// IsCosignSigned checks whether a cosign signature artifact exists in GHCR for
// the given image digest. It confirms the presence of the .sig OCI artifact that
// cosign pushes during keyless signing; it does not re-verify the cryptographic
// signature or the Fulcio certificate chain.
func (c *GitHubClient) IsCosignSigned(ctx context.Context, repo, digest string) (bool, error) {
	if err := validateRepoName(repo); err != nil {
		return false, fmt.Errorf("cosign check: %w", err)
	}
	// Cosign stores keyless signatures as OCI artifacts tagged sha256-<hex>.sig
	sigTag := strings.Replace(digest, ":", "-", 1) + ".sig"
	u, err := safeURL("https://ghcr.io", "v2", c.owner, repo, "manifests", sigTag)
	if err != nil {
		return false, fmt.Errorf("cosign check URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return false, fmt.Errorf("cosign check request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("cosign check fetch: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return resp.StatusCode == http.StatusOK, nil
}
