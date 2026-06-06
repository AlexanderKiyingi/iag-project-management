// Package financeclient calls iag-finance to book AP from approved requisitions.
package financeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	platformserviceauth "github.com/alvor-technologies/iag-platform-go/serviceauth"
)

// Client posts AP open items to iag-finance.
type Client struct {
	baseURL    string
	httpClient *http.Client
	sa         *platformserviceauth.Client
}

// Config wires the finance upstream.
type Config struct {
	BaseURL         string
	TokenURL        string
	ServiceClientID string
	ServiceSecret   string
}

// New returns a Client. When BaseURL is empty the client is disabled.
func New(cfg Config) *Client {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return &Client{}
	}
	var sa *platformserviceauth.Client
	if cfg.ServiceSecret != "" {
		sa = platformserviceauth.NewClient(platformserviceauth.Options{
			TokenURL:     cfg.TokenURL,
			ClientID:     cfg.ServiceClientID,
			ClientSecret: cfg.ServiceSecret,
			Audience:     "iag.finance",
		})
	}
	return &Client{
		baseURL:    base,
		httpClient: &http.Client{Timeout: 12 * time.Second},
		sa:         sa,
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

// CreateAPItem opens an AP line for an approved PM requisition.
func (c *Client) CreateAPItem(ctx context.Context, in CreateAPInput) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("finance client not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"vendorRef":   in.VendorRef,
		"documentRef": in.DocumentRef,
		"description": in.Description,
		"amount":      in.Amount,
		"currency":    in.Currency,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/ap/items", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.sa != nil {
		tok, err := c.sa.Token(ctx)
		if err != nil {
			return "", fmt.Errorf("finance token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("finance ap %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		DocumentRef string `json:"documentRef"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.DocumentRef != "" {
		return out.DocumentRef, nil
	}
	return in.DocumentRef, nil
}

// CreateAPInput is the AP payload for a requisition payout.
type CreateAPInput struct {
	VendorRef   string
	DocumentRef string
	Description string
	Amount      string
	Currency    string
}
