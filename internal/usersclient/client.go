// Package usersclient calls iag-users for org membership and billing identity.
package usersclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to iag-users (typically via the API gateway).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.BaseURL != ""
}

type Organization struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	LegalName   string `json:"legalName"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role,omitempty"`
}

type OrgMember struct {
	UserID   string `json:"userId"`
	Email    string `json:"email,omitempty"`
	FullName string `json:"fullName,omitempty"`
	Role     string `json:"role"`
}

func (c *Client) ListOrgs(ctx context.Context, bearerToken string) ([]Organization, error) {
	var resp struct {
		Items []Organization `json:"items"`
	}
	if err := c.get(ctx, bearerToken, "/v1/orgs", &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetOrg(ctx context.Context, bearerToken, orgID string) (*Organization, error) {
	var org Organization
	if err := c.get(ctx, bearerToken, "/v1/orgs/"+orgID, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

func (c *Client) ListOrgMembers(ctx context.Context, bearerToken, orgID string) ([]OrgMember, error) {
	var resp struct {
		Items []OrgMember `json:"items"`
	}
	if err := c.get(ctx, bearerToken, "/v1/orgs/"+orgID+"/members", &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) get(ctx context.Context, bearerToken, path string, dest any) error {
	if !c.Enabled() {
		return fmt.Errorf("users client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("users api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusForbidden {
		return ErrForbidden
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("users api %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode users response: %w", err)
	}
	return nil
}
