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
}

func (c *Client) request(ctx context.Context, endpoint string, payload any) (any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("cannot encode API request")
	}
	form := url.Values{"key": {c.Key}, "data": {string(data)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/"+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid API request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VyOS %s request failed (check connectivity, TLS trust and router logs)", endpoint)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("VyOS %s returned HTTP %d; consult router logs", endpoint, resp.StatusCode)
	}
	const limit = 32 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(body) > limit {
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
