package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCommitConfirm(t *testing.T) {
	for _, kind := range []string{"single", "batch"} {
		for _, mode := range []string{"create", "update", "delete"} {
			for _, failure := range []string{"", "configure", "verify", "mismatch", "confirm", "recheck", "expired", "save", "skip saving"} {
				t.Run(kind+"/"+mode+"/"+failure, func(t *testing.T) {
					var calls []string
					configured, confirmed := false, false
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						r.ParseForm()
						stage := ""
						data := map[string]any{}
						switch r.URL.Path {
						case "/retrieve":
							stage = "retrieve"
							if configured {
								stage = "verify"
							}
							if confirmed {
								stage = "recheck"
							}
							if !configured && mode != "create" {
								data = map[string]any{"node": map[string]any{"x": "old"}}
							}
							if configured && mode != "delete" {
								data = map[string]any{"node": map[string]any{"x": "new"}}
							}
							if configured && (failure == "mismatch" || confirmed && failure == "expired") {
								data = map[string]any{"node": map[string]any{"x": "SECRET"}}
							}
						case "/configure":
							stage = "configure"
							var payload map[string]any
							if err := json.Unmarshal([]byte(r.FormValue("data")), &payload); err != nil {
								t.Error(err)
							}
							if payload["confirm_time"] != float64(1) || payload["commands"] == nil {
								t.Error("missing timer or commands")
							}
							configured = true
						case "/config-file":
							var payload map[string]any
							json.Unmarshal([]byte(r.FormValue("data")), &payload)
							stage = payload["op"].(string)
							if stage == "confirm" && failure != "confirm" {
								confirmed = true
							}
						default:
							t.Errorf("unexpected endpoint %s", r.URL.Path)
						}
						calls = append(calls, stage)
						if stage == failure {
							json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "SECRET"})
							return
						}
						json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
					}))
					defer server.Close()
					vc := New(&Client{HTTP: server.Client(), Endpoint: server.URL, Key: "test"}, failure == "skip saving", "")
					vc.SetCommitConfirmMinutes(1)
					var err error
					if kind == "single" {
						err = vc.mutate(context.Background(), "node x", "new", mode)
					} else {
						_, err = vc.MutateBatch(context.Background(), Batch{"x": {Path: []string{"node", "x"}, Value: `"new"`}}, mode)
					}
					failed := failure != "" && failure != "skip saving"
					if (err != nil) != failed {
						t.Fatalf("incorrect failure result: %v", err)
					}
					if err != nil && strings.Contains(err.Error(), "SECRET") {
						t.Fatal("secret leaked")
					}
					expected := []string{"retrieve", "configure", "verify", "confirm", "recheck", "save"}
					if failed {
						expected = expected[:map[string]int{"configure": 2, "verify": 3, "mismatch": 3, "confirm": 4, "recheck": 5, "expired": 5, "save": 6}[failure]]
					}
					if failure == "skip saving" {
						expected = expected[:5]
					}
					if !reflect.DeepEqual(calls, expected) {
						t.Fatalf("wrong sequence: %v", calls)
					}
					if failed && failure != "save" {
						var uncertain *MutationError
						if !errors.As(err, &uncertain) {
							t.Fatal("lost uncertain mutation status")
						}
						before := len(calls)
						if err := vc.Update(context.Background(), "node y", "other"); err == nil {
							t.Fatal("continued writes with unresolved confirmation")
						}
						for _, call := range calls[before:] {
							if call == "configure" || call == "confirm" || call == "save" {
								t.Fatal("wrote after unresolved confirmation")
							}
						}
					}
				})
			}
		}
	}
}

func TestCommitConfirmChecksFreshConnections(t *testing.T) {
	var mu sync.Mutex
	configured := false
	initial := ""
	var verification []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		r.ParseForm()
		data := map[string]any{}
		if initial == "" {
			initial = r.RemoteAddr
		}
		if r.URL.Path == "/configure" {
			configured = true
		}
		if r.URL.Path == "/retrieve" && configured {
			verification = append(verification, r.RemoteAddr)
			data = map[string]any{"node": map[string]any{"x": "new"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer server.Close()
	vc := New(&Client{HTTP: server.Client(), Endpoint: server.URL, Key: "test"}, false, "")
	vc.SetCommitConfirmMinutes(1)
	if err := vc.Set(context.Background(), "node x", "new"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(verification) != 2 || verification[0] == initial || verification[1] == verification[0] {
		t.Fatalf("verification reused an established management connection: initial=%s verification=%v", initial, verification)
	}
}
