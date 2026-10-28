package forges

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DetectForgeType probes a domain to identify which forge software it runs.
// It checks HTTP response headers first, then falls back to API endpoints.
// If hc is nil, http.DefaultClient is used.
func DetectForgeType(ctx context.Context, domain string, hc ...*http.Client) (ForgeType, error) {
	client := http.DefaultClient
	if len(hc) > 0 && hc[0] != nil {
		client = hc[0]
	}
	baseURL := "https://" + domain

	ft, err := detectFromHeaders(ctx, client, baseURL)
	if err != nil {
		return Unknown, fmt.Errorf("could not detect forge type for %s: %w", baseURL, err)
	}
	if ft != Unknown {
		return ft, nil
	}

	return detectFromAPI(ctx, client, baseURL)
}

func detectFromHeaders(ctx context.Context, client *http.Client, baseURL string) (ForgeType, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return Unknown, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return Unknown, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Header.Get("X-Forgejo-Version") != "" {
		return Forgejo, nil
	}
	if resp.Header.Get("X-Gitea-Version") != "" {
		return Gitea, nil
	}
	if resp.Header.Get("X-Gitlab-Meta") != "" {
		return GitLab, nil
	}
	if resp.Header.Get("X-GitHub-Request-Id") != "" {
		return GitHub, nil
	}

	return Unknown, nil
}

func detectFromAPI(ctx context.Context, client *http.Client, baseURL string) (ForgeType, error) {
	// Try Gitea/Forgejo /api/v1/version
	if ft, err := probeGiteaAPI(ctx, client, baseURL); err == nil {
		return ft, nil
	}

	// Try GitLab /api/v4/version
	var version struct {
		Version string `json:"version"`
	}
	if err := probeJSON(ctx, client, baseURL+"/api/v4/version", &version); err == nil && strings.TrimSpace(version.Version) != "" {
		return GitLab, nil
	}

	// Try GitHub Enterprise /api/v3/meta
	var meta struct {
		VerifiablePasswordAuthentication *bool `json:"verifiable_password_authentication"`
	}
	if err := probeJSON(ctx, client, baseURL+"/api/v3/meta", &meta); err == nil && meta.VerifiablePasswordAuthentication != nil {
		return GitHub, nil
	}

	if err := ctx.Err(); err != nil {
		return Unknown, err
	}
	return Unknown, fmt.Errorf("could not detect forge type for %s", baseURL)
}

func probeGiteaAPI(ctx context.Context, client *http.Client, baseURL string) (ForgeType, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := probeJSON(ctx, client, baseURL+"/api/v1/version", &v); err != nil {
		return Unknown, err
	}
	if strings.TrimSpace(v.Version) == "" {
		return Unknown, fmt.Errorf("missing version in Gitea API response")
	}

	if strings.Contains(strings.ToLower(v.Version), "forgejo") {
		return Forgejo, nil
	}
	return Gitea, nil
}

func probeJSON(ctx context.Context, client *http.Client, url string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}
