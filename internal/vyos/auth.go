package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tokenCache is shared by all operations on a client. Never copy it after use.
// Expiry or a prior HTTP 401 causes an exchange. Failed requests (especially
// writes) are never replayed; authentication never falls back to a long-lived key.
type tokenCache struct {
	mu        sync.Mutex
	token     string
	refreshAt time.Time
}

func (c *Client) bearer(ctx context.Context) (string, error) {
	if !c.ExchangeToken {
		return c.BearerToken, nil
	}
	c.tokens.mu.Lock()
	defer c.tokens.mu.Unlock()
	if c.tokens.token != "" && time.Now().Before(c.tokens.refreshAt) {
		return c.tokens.token, nil
	}
	// Measure lifetime from before the request, so network time does not extend it.
	started := time.Now()
	form := url.Values{"key": {c.Key}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("invalid token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", errors.New("VyOS token exchange failed (check connectivity, TLS trust and router support)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("VyOS token exchange rejected; check API key and /token support on the router")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return "", errors.New("cannot read token response")
	}
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Token     string `json:"token"`
			ExpiresIn int64  `json:"expires_in"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || !envelope.Success || (len(envelope.Error) > 0 && string(envelope.Error) != "null") || !validBearer(envelope.Data.Token) || envelope.Data.ExpiresIn <= 0 || envelope.Data.ExpiresIn > 31536000 {
		return "", errors.New("invalid VyOS token response")
	}
	lifetime := time.Duration(envelope.Data.ExpiresIn) * time.Second
	// Refresh with 10% of the lifetime remaining, capped at 30 seconds.
	margin := min(lifetime/10, 30*time.Second)
	refreshAt := started.Add(lifetime - margin)
	if !time.Now().Before(refreshAt) {
		return "", errors.New("VyOS token expired during exchange")
	}
	c.tokens.token, c.tokens.refreshAt = envelope.Data.Token, refreshAt
	return c.tokens.token, nil
}

// RFC 6750 b64token, covering JWT and opaque tokens without permitting header
// injection or leaking an invalid credential through a transport error.
func validBearer(token string) bool {
	if token == "" || token[0] == '=' {
		return false
	}
	padding := false
	for _, ch := range token {
		if ch == '=' {
			padding = true
			continue
		}
		if padding || !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("-._~+/", ch)) {
			return false
		}
	}
	return true
}

// ValidBearer reports whether a supplied credential can be used in a Bearer header.
func ValidBearer(token string) bool { return validBearer(token) }
