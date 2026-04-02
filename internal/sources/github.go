package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type cachedToken struct {
	token  string
	expiry time.Time
}

type GitHubClient struct {
	pat        string
	owner      string
	httpClient *http.Client
	tokenMu    sync.Mutex
	tokenCache map[string]cachedToken
}

func NewGitHubClient(pat, owner string) *GitHubClient {
	c := &GitHubClient{
		pat:        pat,
		owner:      owner,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		tokenCache: make(map[string]cachedToken),
	}
	go c.tokenCacheCleanupLoop()
	return c
}

func (c *GitHubClient) tokenCacheCleanupLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		c.tokenMu.Lock()
		for repo, cached := range c.tokenCache {
			if now.After(cached.expiry) {
				delete(c.tokenCache, repo)
			}
		}
		c.tokenMu.Unlock()
	}
}

type WorkflowRun struct {
	Conclusion string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
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
		})
	}

	return runs, nil
}

// ghcrToken exchanges the PAT for a short-lived registry JWT scoped to a single repo.
func (c *GitHubClient) ghcrToken(ctx context.Context, repo string) (string, error) {
	c.tokenMu.Lock()
	if cached, ok := c.tokenCache[repo]; ok && time.Now().Before(cached.expiry) {
		c.tokenMu.Unlock()
		return cached.token, nil
	}
	c.tokenMu.Unlock()

	u := fmt.Sprintf("https://ghcr.io/token?service=ghcr.io&scope=repository:%s/%s:pull", c.owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("ghcr token request: %w", err)
	}
	req.SetBasicAuth(c.owner, c.pat)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ghcr token fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("ghcr token read: %w", err)
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("ghcr token parse: %w", err)
	}
	if result.Token == "" {
		return "", fmt.Errorf("ghcr token: empty response")
	}

	ttl := time.Duration(result.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 4 * time.Minute
	}
	c.tokenMu.Lock()
	c.tokenCache[repo] = cachedToken{token: result.Token, expiry: time.Now().Add(ttl - 30*time.Second)}
	c.tokenMu.Unlock()

	return result.Token, nil
}

// ImageManifest fetches the OCI manifest for the :latest tag of a ghcr.io image.
func (c *GitHubClient) ImageManifest(ctx context.Context, repo string) (digest string, sizeBytes int64, err error) {
	if err := validateRepoName(repo); err != nil {
		return "", 0, fmt.Errorf("ghcr manifest: %w", err)
	}
	token, err := c.ghcrToken(ctx, repo)
	if err != nil {
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
	req.Header.Set("Authorization", "Bearer "+token)
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
	if resp.StatusCode != http.StatusOK {
		return digest, 0, fmt.Errorf("ghcr manifest: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var manifest struct {
		MediaType string `json:"mediaType"`
		Config    struct {
			Size int64 `json:"size"`
		} `json:"config"`
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
		// Image index fields
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS   string `json:"os"`
				Arch string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return digest, 0, fmt.Errorf("ghcr manifest parse: %w", err)
	}

	// If this is a multi-arch index, follow the linux/amd64 platform manifest.
	if len(manifest.Manifests) > 0 {
		var platformDigest string
		for _, m := range manifest.Manifests {
			if m.Platform.OS == "linux" && m.Platform.Arch == "amd64" {
				platformDigest = m.Digest
				break
			}
		}
		if platformDigest == "" {
			platformDigest = manifest.Manifests[0].Digest
		}
		_, size, err := c.fetchManifestDigest(ctx, repo, platformDigest, token)
		return digest, size, err
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
	token, err := c.ghcrToken(ctx, repo)
	if err != nil {
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
	req.Header.Set("Authorization", "Bearer "+token)
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

	var semverTags []string
	for _, tag := range tagList.Tags {
		if semverRe.MatchString(tag) {
			semverTags = append(semverTags, tag)
		}
	}

	type tagResult struct {
		tag    string
		digest string
	}
	results := make([]tagResult, len(semverTags))
	var wg sync.WaitGroup
	for i, tag := range semverTags {
		i, tag := i, tag
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, _, _ := c.fetchManifestDigest(ctx, repo, tag, token)
			results[i] = tagResult{tag: tag, digest: d}
		}()
	}
	wg.Wait()

	for _, r := range results {
		if r.digest == latestDigest {
			return r.tag, nil
		}
	}
	return "", nil
}

func (c *GitHubClient) fetchManifestDigest(ctx context.Context, repo, tag, token string) (string, int64, error) {
	u, err := safeURL("https://ghcr.io", "v2", c.owner, repo, "manifests", tag)
	if err != nil {
		return "", 0, fmt.Errorf("ghcr manifest digest URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", 0, err
	}

	var m struct {
		Config struct {
			Size int64 `json:"size"`
		} `json:"config"`
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return resp.Header.Get("Docker-Content-Digest"), 0, nil
	}

	var total int64
	total += m.Config.Size
	for _, l := range m.Layers {
		total += l.Size
	}

	return resp.Header.Get("Docker-Content-Digest"), total, nil
}

// IsCosignSigned checks the Rekor transparency log for a keyless cosign signature
// matching the given image digest. All keyless cosign signatures are publicly
// logged in Rekor, making this more reliable than querying GHCR's OCI API directly.
func (c *GitHubClient) IsCosignSigned(ctx context.Context, digest string) (bool, error) {
	body, err := json.Marshal(map[string]string{"hash": digest})
	if err != nil {
		return false, fmt.Errorf("rekor request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://rekor.sigstore.dev/api/v1/index/retrieve",
		bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("rekor request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("rekor fetch: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return false, fmt.Errorf("rekor read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var uuids []string
	if err := json.Unmarshal(respBody, &uuids); err != nil {
		return false, fmt.Errorf("rekor parse: %w", err)
	}
	return len(uuids) > 0, nil
}

// RepoHomepage returns the homepage URL set on a GitHub repo, or empty string if unset.
func (c *GitHubClient) RepoHomepage(ctx context.Context, repo string) (string, error) {
	if err := validateRepoName(repo); err != nil {
		return "", fmt.Errorf("repo homepage: %w", err)
	}
	u, err := safeURL("https://api.github.com", "repos", c.owner, repo)
	if err != nil {
		return "", fmt.Errorf("repo homepage URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("repo homepage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("repo homepage fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("repo homepage read: %w", err)
	}

	var result struct {
		Homepage string `json:"homepage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("repo homepage parse: %w", err)
	}
	return result.Homepage, nil
}

// LastPushInfo holds the repo of the most recent push event by the owner.
type LastPushInfo struct {
	Repo string
}

// LastPush returns the repo of the most recent PushEvent for the owner.
func (c *GitHubClient) LastPush(ctx context.Context) (LastPushInfo, error) {
	u, err := safeURL("https://api.github.com", "users", c.owner, "events")
	if err != nil {
		return LastPushInfo{}, fmt.Errorf("last push URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?per_page=30", nil)
	if err != nil {
		return LastPushInfo{}, fmt.Errorf("last push request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return LastPushInfo{}, fmt.Errorf("last push fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return LastPushInfo{}, fmt.Errorf("last push status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return LastPushInfo{}, fmt.Errorf("last push read: %w", err)
	}

	var events []struct {
		Type string `json:"type"`
		Repo struct {
			Name string `json:"name"`
		} `json:"repo"`
	}
	if err := json.Unmarshal(body, &events); err != nil {
		return LastPushInfo{}, fmt.Errorf("last push parse: %w", err)
	}

	for _, e := range events {
		if e.Type != "PushEvent" {
			continue
		}
		// repo name is "owner/repo" — strip the owner prefix
		repoName := strings.TrimPrefix(e.Repo.Name, c.owner+"/")
		return LastPushInfo{Repo: repoName}, nil
	}
	return LastPushInfo{}, nil
}
