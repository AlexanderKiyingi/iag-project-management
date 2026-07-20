// Package chatclient is a thin Go client other microservices import to attach
// chat threads to their domain objects and post automated system messages —
// Tier 2 integration (see docs/iag-chat-service-design.md §8).
//
// It calls the chat service's /internal endpoints service-to-service using a
// serviceauth.Client (aud=iag.chat). Mirrors the shape of the existing
// procurementclient / aiclient helpers.
package chatclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alvor-technologies/iag-platform-go/serviceauth"
)

// Link anchors a conversation to a microservice domain object.
type Link struct {
	Service    string `json:"service"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
}

// Client posts to the chat service's internal API.
type Client struct {
	baseURL string
	auth    *serviceauth.Client
	http    *http.Client
}

// Options configures a Client.
type Options struct {
	// BaseURL is the chat service base URL (direct or via gateway prefix),
	// e.g. "http://chat:8085" or "https://<gateway>/api/v1/chat".
	BaseURL string
	// TokenURL, ClientID, ClientSecret authenticate this service to auth.
	TokenURL     string
	ClientID     string
	ClientSecret string
	// HTTPClient overrides the default. Optional.
	HTTPClient *http.Client
}

// New builds a chat client. Audience is fixed to iag.chat.
func New(opts Options) *Client {
	httpC := opts.HTTPClient
	if httpC == nil {
		httpC = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		auth: serviceauth.NewClient(serviceauth.Options{
			TokenURL:     opts.TokenURL,
			ClientID:     opts.ClientID,
			ClientSecret: opts.ClientSecret,
			Audience:     "iag.chat",
		}),
		http: httpC,
	}
}

// Conversation is the minimal shape returned by the internal endpoints.
type Conversation struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// UpsertThread find-or-creates the canonical thread for a linked entity and
// optionally seeds participant user ids.
func (c *Client) UpsertThread(ctx context.Context, link Link, title string, participants ...string) (Conversation, error) {
	body := map[string]any{"link": link, "participants": participants}
	if title != "" {
		body["title"] = title
	}
	var conv Conversation
	err := c.post(ctx, "/internal/threads", body, &conv)
	return conv, err
}

// PostSystem posts a system message into the thread for a linked entity,
// find-or-creating the thread if needed.
func (c *Client) PostSystem(ctx context.Context, link Link, message string) error {
	body := map[string]any{"link": link, "body": message}
	return c.post(ctx, "/internal/threads/messages", body, nil)
}

func (c *Client) post(ctx context.Context, path string, payload any, out any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("chatclient: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("chatclient: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.auth.AuthorizeRequest(ctx, req); err != nil {
		return fmt.Errorf("chatclient: authorize: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("chatclient: post %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatclient: %s status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
