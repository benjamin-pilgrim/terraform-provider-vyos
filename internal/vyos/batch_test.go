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
)

type batchFixture struct {
	mu      sync.Mutex
	tree    map[string]any
	calls   []string
	ops     []map[string]any
	failure string
	save    map[string]any
}

func newBatchFixture(t *testing.T, skip bool, file string) (*VyosConfig, *batchFixture) {
	t.Helper()
	fixture := &batchFixture{tree: map[string]any{"unrelated": "keep"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.calls = append(fixture.calls, r.URL.Path)
		if r.URL.Path == "/"+fixture.failure {
			fmt.Fprint(w, `{"success":false,"error":"SECRET"}`)
			return
		}
		r.ParseForm()
		var data any
		switch r.URL.Path {
		case "/retrieve":
			data = fixture.tree
		case "/configure":
			if err := json.Unmarshal([]byte(r.FormValue("data")), &fixture.ops); err != nil {
				t.Error(err)
			}
			for _, op := range fixture.ops {
				parts := op["path"].([]any)
				node := fixture.tree
				for _, part := range parts[:len(parts)-1] {
					if node[part.(string)] == nil {
						node[part.(string)] = map[string]any{}
					}
					node = node[part.(string)].(map[string]any)
				}
				last := parts[len(parts)-1].(string)
				if op["op"] == "delete" {
					delete(node, last)
				} else if value, present := op["value"]; present {
					node[last] = value
				} else {
					node[last] = map[string]any{}
				}
			}
		case "/config-file":
			json.Unmarshal([]byte(r.FormValue("data")), &fixture.save)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data, "error": nil})
	}))
	t.Cleanup(server.Close)
	return New(&Client{HTTP: server.Client(), Endpoint: server.URL, Key: "test"}, skip, file), fixture
}

func sampleBatch() Batch {
	return Batch{
		"first": {Path: []string{"node", "has space"}, Value: `{"leaf":"one"}`},
		"flag":  {Path: []string{"node", "flag"}, Value: `{}`},
	}
}

func TestBatchOneCommitAndSave(t *testing.T) {
	for _, mode := range []string{"create", "update", "delete"} {
		for _, saving := range []string{"default", "custom", "skip"} {
			t.Run(mode+"/"+saving, func(t *testing.T) {
				file := ""
				if saving == "custom" {
					file = "/config/custom.boot"
				}
				vc, fixture := newBatchFixture(t, saving == "skip", file)
				if mode != "create" {
					fixture.tree["node"] = map[string]any{"has space": map[string]any{"leaf": "old"}, "flag": map[string]any{}}
				}
				attempted, err := vc.MutateBatch(context.Background(), sampleBatch(), mode)
				if err != nil || !attempted {
					t.Fatalf("mutation: attempted=%v error=%v", attempted, err)
				}
				expected := []string{"/retrieve", "/configure"}
				if saving != "skip" {
					expected = append(expected, "/config-file")
					if fixture.save["op"] != "save" || (file != "" && fixture.save["file"] != file) {
						t.Fatalf("incorrect save: %v", fixture.save)
					}
				}
				if !reflect.DeepEqual(fixture.calls, expected) {
					t.Fatalf("expected one snapshot/commit/save, got %v", fixture.calls)
				}
				if fixture.tree["unrelated"] != "keep" {
					t.Fatal("unrelated configuration changed")
				}
				if mode == "delete" {
					if len(vc.managedPaths) != 0 || len(fixture.ops) != 2 {
						t.Fatal("batch deletion did not release all paths")
					}
				} else {
					value, _ := configNode(fixture.tree, []string{"node", "has space", "leaf"})
					if value != "one" {
						t.Fatalf("array component was split or not written: %v", value)
					}
					if mode == "update" && len(fixture.ops) != 2 {
						t.Fatal("unchanged flag was unnecessarily rewritten")
					}
				}
			})
		}
	}
}

func TestBatchPreflightRejectsExistingAndInvalidPaths(t *testing.T) {
	for _, name := range []string{"existing", "invalid value", "overlap", "duplicate", "empty component", "empty batch"} {
		t.Run(name, func(t *testing.T) {
			vc, fixture := newBatchFixture(t, false, "")
			entries := sampleBatch()
			switch name {
			case "existing":
				fixture.tree["node"] = map[string]any{"flag": map[string]any{}}
			case "invalid value":
				entries["flag"] = BatchEntry{Path: []string{"node", "flag"}, Value: `null`}
			case "overlap":
				entries["parent"] = BatchEntry{Path: []string{"node"}, Value: `{}`}
			case "duplicate":
				entries["copy"] = entries["flag"]
			case "empty component":
				entries["flag"] = BatchEntry{Path: []string{"node", ""}, Value: `{}`}
			case "empty batch":
				entries = Batch{}
			}
			attempted, err := vc.MutateBatch(context.Background(), entries, "create")
			if err == nil || attempted {
				t.Fatalf("unsafe batch accepted: attempted=%v error=%v", attempted, err)
			}
			for _, call := range fixture.calls {
				if call != "/retrieve" {
					t.Fatal("unsafe request wrote configuration")
				}
			}
		})
	}
}

func TestBatchFailuresAndReadDrift(t *testing.T) {
	for _, endpoint := range []string{"retrieve", "configure", "config-file"} {
		t.Run(endpoint, func(t *testing.T) {
			vc, fixture := newBatchFixture(t, false, "")
			fixture.failure = endpoint
			attempted, err := vc.MutateBatch(context.Background(), sampleBatch(), "create")
			if err == nil || attempted != (endpoint != "retrieve") || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("incorrect failure handling: attempted=%v error=%v", attempted, err)
			}
		})
	}
	vc, fixture := newBatchFixture(t, false, "")
	entries := sampleBatch()
	if _, err := vc.MutateBatch(context.Background(), entries, "create"); err != nil {
		t.Fatal(err)
	}
	fixture.tree["node"].(map[string]any)["has space"] = map[string]any{"leaf": "external"}
	delete(fixture.tree["node"].(map[string]any), "flag")
	fixture.calls = nil
	current, err := vc.ShowBatch(context.Background(), entries)
	if err != nil || current["first"].(map[string]any)["leaf"] != "external" || len(current) != 1 || len(fixture.calls) != 1 {
		t.Fatalf("batch did not refresh drift/deletion with one snapshot: %v, %v", current, err)
	}
	// Only the absent entry's claim is released; the surviving path stays owned.
	if _, err := vc.Show(context.Background(), "node flag"); err != nil {
		t.Fatal(err)
	}
	if _, err := vc.Show(context.Background(), "node has space"); err != nil {
		// This is a different three-component path, not the two-component tag.
		t.Fatal(err)
	}
}

func TestBatchAndIndividualOwnership(t *testing.T) {
	for _, batchFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(batchFirst), func(t *testing.T) {
			vc, _ := newBatchFixture(t, false, "")
			entries := Batch{"flag": {Path: []string{"node", "flag"}, Value: `{}`}}
			if batchFirst {
				if _, err := vc.MutateBatch(context.Background(), entries, "create"); err != nil {
					t.Fatal(err)
				}
				if _, err := vc.Show(context.Background(), "node flag"); err == nil {
					t.Fatal("individual resource adopted a batch-owned path")
				}
			} else {
				if err := vc.Update(context.Background(), "node flag", map[string]any{}); err != nil {
					t.Fatal(err)
				}
				if _, err := vc.ShowBatch(context.Background(), entries); err == nil {
					t.Fatal("batch adopted an individual resource path")
				}
			}
		})
	}
}

func TestBatchIdentityRoundTrip(t *testing.T) {
	entries := sampleBatch()
	id, err := BatchIdentity(entries)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseBatchIdentity(id)
	if err != nil || !reflect.DeepEqual(entries["first"].Path, decoded["first"].Path) {
		t.Fatalf("identity lost path components: %v", err)
	}
	if _, err := ParseBatchIdentity(`{"invalid":"not an array"}`); err == nil {
		t.Fatal("invalid import accepted")
	}
}
