package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// GitHub opens an issue when an alert fires.
//
// apiBaseUrl is a credential rather than a constant because GitHub Enterprise
// Server lives on the customer's own host, where the API sits under /api/v3.
type GitHub struct{}

// NewGitHub returns a new GitHub caller.
func NewGitHub() *GitHub { return &GitHub{} }

// Send creates an issue on the configured repository.
func (p *GitHub) Send(ctx context.Context, creds map[string]string, payload integrationbus.AlertPayload) error {
	owner := creds["owner"]
	repo := creds["repo"]
	token := creds["token"]

	switch {
	case owner == "":
		return fmt.Errorf("github: missing owner credential")
	case repo == "":
		return fmt.Errorf("github: missing repo credential")
	case token == "":
		return fmt.Errorf("github: missing token credential")
	}

	base := strings.TrimRight(creds["apiBaseUrl"], "/")
	if base == "" {
		base = githubBase
	}

	body := map[string]any{
		"title": fmt.Sprintf("[%s] %s: %s", payload.Level, payload.ProjectName, payload.Message),
		"body": fmt.Sprintf(
			"**Level:** %s\n**Source:** %s\n**Time:** %s\n**Log ID:** %s\n\n%s\n\n_Opened by Streamlogia._",
			payload.Level,
			payload.Source,
			payload.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
			payload.LogID,
			payload.Message,
		),
	}

	// Labels are optional and comma-separated. A label that does not exist on the
	// repository is created by GitHub rather than rejected.
	if labels := strings.TrimSpace(creds["labels"]); labels != "" {
		var out []string
		for _, l := range strings.Split(labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		if len(out) > 0 {
			body["labels"] = out
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("github: marshal: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/issues", base, owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("github: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		// 403 with "Issues are disabled" and 404 for a token that cannot see the
		// repository are the two common misconfigurations, and both need the
		// message to be distinguishable.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, string(b))
	}

	return nil
}
