package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Real Terraform verifies first apply, import, no-op plans, pruning repair,
// replacement and retained shared cache through the public API contract.
func TestAccContainerImage(t *testing.T) {
	var mu sync.Mutex
	images := map[string]bool{}
	pulls := 0
	image1 := "ghcr.io/example/boot@sha256:" + strings.Repeat("a", 64)
	image2 := "ghcr.io/example/boot@sha256:" + strings.Repeat("b", 64)
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		r.ParseForm()
		var data struct {
			Op, Name string
			Path     []string
		}
		if err := json.Unmarshal([]byte(r.FormValue("data")), &data); err != nil {
			t.Error(err)
		}
		var result any
		switch r.URL.Path {
		case "/show":
			if data.Op != "show" || strings.Join(data.Path, " ") != "container image json" {
				t.Errorf("unexpected show operation: %+v", data)
			}
			rows := []map[string]any{}
			for name, present := range images {
				if present {
					rows = append(rows, map[string]any{"repo_digests": []string{name}})
				}
			}
			encoded, _ := json.Marshal(rows)
			result = string(encoded)
		case "/container-image":
			if data.Op != "add" {
				t.Errorf("unexpected image operation: %s", data.Op)
			}
			images[data.Name] = true
			pulls++
			result = ""
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.Error(w, "unexpected endpoint", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": result})
	}))
	defer api.Close()
	for _, name := range []string{"VYOS_AUTH_MODE", "VYOS_BEARER_TOKEN", "VYOS_CA_CERTIFICATE_FILE", "VYOS_CLIENT_CERTIFICATE_FILE", "VYOS_CLIENT_KEY_FILE", "VYOS_SKIP_SAVING"} {
		t.Setenv(name, "")
	}
	config := func(image string) string {
		return fmt.Sprintf(`
provider "vyos" {
 endpoint = %q
 auth_mode = "api_key"
 api_key = "fixture"
 insecure = true
}
resource "vyos_container_image" "boot" { image = %q }
`, api.URL, image)
	}
	check := func(want int, image string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if pulls != want || !images[image] {
				return fmt.Errorf("pulls=%d want=%d, cached=%v", pulls, want, images[image])
			}
			if got := s.RootModule().Resources["vyos_container_image.boot"].Primary.ID; got != image {
				return fmt.Errorf("id=%s want=%s", got, image)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if !images[image1] || !images[image2] || pulls != 3 {
				return fmt.Errorf("destroy changed shared cache: %v; pulls %d", images, pulls)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config(image1), Check: check(1, image1)},
			{ResourceName: "vyos_container_image.boot", ImportState: true, ImportStateVerify: true},
			{Config: config(image1), PlanOnly: true, Check: check(1, image1)},
			{PreConfig: func() { mu.Lock(); delete(images, image1); mu.Unlock() }, Config: config(image1), Check: check(2, image1)},
			{Config: config(image2), Check: check(3, image2)},
		},
	})
}
