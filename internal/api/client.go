// Package api is the agent's HTTP client for talking to the control server.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/protocol"
)

// Client talks to the control server.
type Client struct {
	baseURL string
	token   string
	agentID string
	http    *http.Client
}

// New creates a control-server client.
func New(baseURL, token, agentID string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		agentID: agentID,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Poll sends a heartbeat and returns any pending actions.
func (c *Client) Poll(ctx context.Context, req protocol.PollRequest) (protocol.PollResponse, error) {
	var resp protocol.PollResponse
	err := c.do(ctx, "/api/agent/poll", req, &resp)
	return resp, err
}

// ReportResult reports the outcome of a single action.
func (c *Client) ReportResult(ctx context.Context, res protocol.ActionResult) error {
	return c.do(ctx, "/api/agent/result", res, nil)
}

func (c *Client) do(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Agent-Id", c.agentID)

	httpResp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return fmt.Errorf("%s: server returned %d: %s", path, httpResp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s: decode response: %w", path, err)
		}
	}
	return nil
}
