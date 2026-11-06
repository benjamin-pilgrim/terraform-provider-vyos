package vyos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client deliberately never returns response bodies or transport errors: VyOS
// errors can echo configuration values and credentials supplied in a request.
type Client struct {
	HTTP          *http.Client
	Endpoint, Key string
	BearerToken   string
	ExchangeToken bool
	tokens        tokenCache
}

func (c *Client) request(ctx context.Context, endpoint string, payload any) (any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("cannot encode API request")
	}
	token, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{"data": {string(data)}}
	if token == "" && c.Key != "" {
		form.Set("key", c.Key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/"+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid API request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("VyOS %s request did not complete: %w", endpoint, ctx.Err())
		}
		return nil, fmt.Errorf("VyOS %s request failed (check connectivity, TLS trust and router logs)", endpoint)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && c.ExchangeToken {
		// A router restart rotates its signing secret. Forget the cached token for
		// the next operation, but do not replay this potentially mutating request.
		c.tokens.mu.Lock()
		if c.tokens.token == token {
			c.tokens.token = ""
		}
		c.tokens.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("VyOS %s returned HTTP %d; consult router logs", endpoint, resp.StatusCode)
	}
	const limit = 32 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(body) > limit {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("VyOS %s response did not complete: %w", endpoint, ctx.Err())
		}
		return nil, errors.New("cannot read API response")
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    any             `json:"data"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, errors.New("invalid JSON API response")
	}
	if envelope.Success == nil || !*envelope.Success || (len(envelope.Error) > 0 && !bytes.Equal(envelope.Error, []byte("null"))) {
		return nil, fmt.Errorf("VyOS %s rejected request; consult router logs", endpoint)
	}
	if endpoint == "configure" {
		if message, ok := envelope.Data.(string); ok && strings.Contains(message, asyncCommitMessage) {
			return nil, errors.New("VyOS accepted an asynchronous commit that cannot be verified; it may still execute. No confirmation or save was sent; inspect the router before retrying")
		}
	}
	return envelope.Data, nil
}
