package vyos_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
)

func TestContainerImageAPI(t *testing.T) {
	image := "ghcr.io/example/boot@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		listing any
		cached  bool
		failure bool
	}{
		{"missing", `[]`, false, false},
		{"cached", `[{"repo_digests":["` + image + `"]}]`, true, false},
		{"structured response", []any{map[string]any{"RepoDigests": []string{image}}}, true, false},
		{"same digest other repository", `[{"repo_digests":["other.example/boot@sha256:` + strings.Repeat("a", 64) + `"]}]`, false, false},
		{"no repository digests", `[{"names":["ghcr.io/example/boot"]}]`, false, true},
		{"malformed listing", `not json`, false, true},
		{"wrong response shape", map[string]any{}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listing := tc.listing
			pulls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.FormValue("key") != "test-key" {
					t.Error("missing API authentication")
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(r.FormValue("data")), &payload); err != nil {
					t.Error(err)
				}
				switch r.URL.Path {
				case "/show":
					if got := payload["path"]; strings.Join(asStrings(t, got), " ") != "container image json" {
						t.Errorf("unexpected show path: %v", got)
					}
					json.NewEncoder(w).Encode(map[string]any{"success": true, "data": listing})
				case "/container-image":
					if payload["op"] != "add" || payload["name"] != image {
						t.Errorf("unexpected pull: %v", payload)
					}
					pulls++
					listing = `[{"repo_digests":["` + image + `"]}]`
					json.NewEncoder(w).Encode(map[string]any{"success": true, "data": ""})
				default:
					t.Errorf("unexpected operation: %s", r.URL.Path)
					http.Error(w, "unexpected operation", 500)
				}
			}))
			defer api.Close()
			client := vyos.New(&vyos.Client{HTTP: api.Client(), Endpoint: api.URL, Key: "test-key"}, false, "")
			err := client.EnsureContainerImage(context.Background(), image)
			if (err != nil) != tc.failure {
				t.Fatalf("error=%v, want failure=%v", err, tc.failure)
			}
			want := 1
			if tc.cached || tc.failure {
				want = 0
			}
			if pulls != want {
				t.Fatalf("pulls=%d, want %d", pulls, want)
			}
			if !tc.failure {
				if err := client.EnsureContainerImage(context.Background(), image); err != nil {
					t.Fatal(err)
				}
				if pulls != want {
					t.Fatal("cached image was pulled again")
				}
			}
		})
	}
}

func asStrings(t *testing.T, value any) []string {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("expected path array, got %T", value)
	}
	result := make([]string, len(values))
	for i, v := range values {
		result[i], ok = v.(string)
		if !ok {
			t.Fatal("expected string path")
		}
	}
	return result
}

func TestContainerImageReferences(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, ref := range []string{"ghcr.io/example/boot@sha256:" + digest, "localhost:5000/boot@sha256:" + digest} {
		if err := vyos.ValidateContainerImage(ref); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
	for _, ref := range []string{"all", "ghcr.io/example/boot:latest", "boot@sha256:" + digest, "ghcr.io/example/boot@sha256:abc", "ghcr.io/example/boot@sha256:" + digest + ";id", "ghcr.io/$(id)/boot@sha256:" + digest, "ghcr.io/example/boot@sha256:" + digest + "\n"} {
		if err := vyos.ValidateContainerImage(ref); err == nil {
			t.Errorf("accepted unsafe or mutable image reference %q", ref)
		}
	}
}

func TestContainerImagePullMustBeVerified(t *testing.T) {
	image := "ghcr.io/example/boot@sha256:" + strings.Repeat("a", 64)
	for _, failPull := range []bool{false, true} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/container-image" && failPull {
				json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "PRIVATE-REGISTRY-CREDENTIAL"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": "[]"})
		}))
		client := vyos.New(&vyos.Client{HTTP: api.Client(), Endpoint: api.URL, Key: "SECRET"}, false, "")
		err := client.EnsureContainerImage(context.Background(), image)
		api.Close()
		if err == nil {
			t.Fatal("accepted a failed or unverified pull")
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "PRIVATE-REGISTRY-CREDENTIAL") {
			t.Fatal("error leaked credentials")
		}
	}
}
