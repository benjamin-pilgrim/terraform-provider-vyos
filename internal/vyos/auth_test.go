package vyos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuthenticationAcrossCommitConfirm(t *testing.T) {
	for _, mode := range []string{"api_key", "bearer", "mtls", "api_key_token"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			configured := false
			var calls []string
			var verification []string
			initial := ""
			exchanges := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.URL.Path == "/token" {
					exchanges++
					if mode != "api_key_token" || r.Form.Get("key") != "SECRET" || r.Header.Get("Authorization") != "" {
						t.Error("unexpected token credentials")
					}
					fmt.Fprint(w, `{"success":true,"data":{"token":"signed.jwt.token","expires_in":3600},"error":null}`)
					return
				}
				wantKey, wantAuth := "", ""
				if mode == "api_key" {
					wantKey = "SECRET"
				}
				if mode == "bearer" || mode == "api_key_token" {
					wantAuth = "Bearer signed.jwt.token"
				}
				if r.Form.Get("key") != wantKey || r.Header.Get("Authorization") != wantAuth {
					t.Error("incorrect credentials on", r.URL.Path)
				}
				if mode != "api_key" && r.Form.Has("key") {
					t.Error("key sent in non-key mode")
				}
				if initial == "" {
					initial = r.RemoteAddr
				}
				data := map[string]any{}
				stage := strings.TrimPrefix(r.URL.Path, "/")
				if r.URL.Path == "/configure" {
					configured = true
				}
				if r.URL.Path == "/retrieve" && configured {
					verification = append(verification, r.RemoteAddr)
					data = map[string]any{"node": map[string]any{"x": "new"}}
				}
				if r.URL.Path == "/config-file" {
					var op struct {
						Op string `json:"op"`
					}
					if err := json.Unmarshal([]byte(r.Form.Get("data")), &op); err != nil {
						t.Error(err)
					}
					stage = op.Op
				}
				calls = append(calls, stage)
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data, "error": nil})
			}))
			defer s.Close()
			client := &Client{HTTP: s.Client(), Endpoint: s.URL}
			switch mode {
			case "api_key":
				client.Key = "SECRET"
			case "bearer":
				client.BearerToken = "signed.jwt.token"
			case "api_key_token":
				client.Key = "SECRET"
				client.ExchangeToken = true
			}
			// Actual mTLS handshakes and certificate validation are covered in provider
			// tests. Here it must omit both application-layer credentials on every call.
			vc := New(client, false, "")
			vc.SetCommitConfirmMinutes(1)
			if err := vc.Set(context.Background(), "node x", "new"); err != nil {
				t.Fatal(err)
			}
			want := []string{"retrieve", "configure", "retrieve", "confirm", "retrieve", "save"}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls %v", calls)
			}
			if len(verification) != 2 || verification[0] == initial || verification[1] == verification[0] {
				t.Fatal("verification reused connections")
			}
			if (mode == "api_key_token" && exchanges != 1) || (mode != "api_key_token" && exchanges != 0) {
				t.Fatal("incorrect token reuse")
			}
		})
	}
}

func TestTokenRefreshAndUnauthorizedDoesNotReplay(t *testing.T) {
	exchanges, requests := 0, 0
	reject := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			exchanges++
			fmt.Fprintf(w, `{"success":true,"data":{"token":"token%d","expires_in":60}}`, exchanges)
			return
		}
		requests++
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer token%d", exchanges) {
			t.Error("stale token")
		}
		if reject {
			w.WriteHeader(401)
			fmt.Fprint(w, "SECRET")
			return
		}
		fmt.Fprint(w, `{"success":true,"data":{}}`)
	}))
	defer s.Close()
	c := &Client{HTTP: s.Client(), Endpoint: s.URL, Key: "SECRET", ExchangeToken: true}
	request := func(wantErr bool) {
		t.Helper()
		_, err := c.request(context.Background(), "configure", nil)
		if (err != nil) != wantErr {
			t.Fatalf("request: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Fatal("secret leaked")
		}
	}
	started := time.Now()
	request(false)
	if c.tokens.refreshAt.Before(started.Add(54*time.Second)) || c.tokens.refreshAt.After(time.Now().Add(54*time.Second)) {
		t.Fatal("expiry safety margin not applied")
	}
	request(false)
	if exchanges != 1 {
		t.Fatal("unnecessary exchange")
	}
	c.tokens.mu.Lock()
	c.tokens.refreshAt = time.Now().Add(-time.Second)
	c.tokens.mu.Unlock()
	request(false)
	if exchanges != 2 {
		t.Fatal("expired token reused")
	}
	reject = true
	request(true)
	if requests != 4 || exchanges != 2 {
		t.Fatal("unauthorized request replayed")
	}
	reject = false
	request(false)
	if exchanges != 3 {
		t.Fatal("rejected token retained")
	}
}

func TestInvalidTokenResponsesNeverSendAPIRequest(t *testing.T) {
	for _, body := range []string{
		`SECRET`, `{"success":false,"error":"SECRET"}`, `{"success":true,"data":{"token":"SECRET","expires_in":0}}`,
		`{"success":true,"data":{"token":"SECRET","expires_in":-1}}`, `{"success":true,"data":{"token":"SECRET","expires_in":31536001}}`,
		`{"success":true,"data":{"token":"SECRET","expires_in":1.5}}`, `{"success":true,"data":{"token":"SECRET","expires_in":"60"}}`,
		`{"success":true,"data":{"token":"SECRET\r\nX-Evil: yes","expires_in":60}}`,
		`{"success":true,"data":{"access_token":"SECRET","expires_in":60}}`,
		`{"success":true,"data":{"token":"SECRET","expires_in":60},"error":"SECRET"}`,
		strings.Repeat("SECRET", 180000),
	} {
		t.Run(fmt.Sprintf("length%d", len(body)), func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/token" {
					t.Error("API called after failed exchange")
				}
				fmt.Fprint(w, body)
			}))
			defer s.Close()
			c := &Client{HTTP: s.Client(), Endpoint: s.URL, Key: "SECRET", ExchangeToken: true}
			_, err := c.request(context.Background(), "configure", nil)
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 1 {
				t.Fatal("invalid exchange or leaked secret", err)
			}
		})
	}
	for _, status := range []int{301, 401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, "SECRET") }))
			defer s.Close()
			c := &Client{HTTP: s.Client(), Endpoint: s.URL, Key: "SECRET", ExchangeToken: true}
			if _, err := c.request(context.Background(), "retrieve", nil); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("exchange failure not sanitized", err)
			}
		})
	}
}

func TestConcurrentTokenExchange(t *testing.T) {
	var mu sync.Mutex
	exchanges := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			mu.Lock()
			exchanges++
			mu.Unlock()
			fmt.Fprint(w, `{"success":true,"data":{"token":"shared","expires_in":60}}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer shared" {
			t.Error("missing shared token")
		}
		fmt.Fprint(w, `{"success":true,"data":{}}`)
	}))
	defer s.Close()
	c := &Client{HTTP: s.Client(), Endpoint: s.URL, Key: "SECRET", ExchangeToken: true}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := c.request(context.Background(), "retrieve", nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if exchanges != 1 {
		t.Fatalf("%d exchanges", exchanges)
	}
}

func TestBearerSyntax(t *testing.T) {
	for _, token := range []string{"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.signature", "opaque+token/=", "abc-._~+/0123=="} {
		if !ValidBearer(token) {
			t.Fatal("valid token rejected")
		}
	}
	for _, token := range []string{"", "=", "==", "abc=def", "Bearer abc", " abc", "abc\n", "abc\r\nHeader: value", "token\x00", "tökén"} {
		if ValidBearer(token) {
			t.Fatal("invalid token accepted")
		}
	}
}
