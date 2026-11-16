package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Real Terraform exercises plans, selector updates, refresh and destroy against
// an isolated API fixture. Router writes are observable, including hidden churn.
func TestAccSelectorTransitions(t *testing.T) {
	var mu sync.Mutex
	tree := map[string]any{"node": map[string]any{
		"list":    []any{"owned", "foreign"},
		"adopted": map[string]any{"leaf": "live", "unowned": "keep"},
	}}
	writes := 0
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/configure" {
			writes++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			var ops []struct {
				Op    string   `json:"op"`
				Path  []string `json:"path"`
				Value *string  `json:"value"`
			}
			if err := json.Unmarshal([]byte(r.FormValue("data")), &ops); err != nil {
				t.Error(err)
			}
			for _, op := range ops {
				node := tree
				for _, part := range op.Path[:len(op.Path)-1] {
					if node[part] == nil {
						node[part] = map[string]any{}
					}
					node = node[part].(map[string]any)
				}
				leaf := op.Path[len(op.Path)-1]
				if op.Op == "delete" {
					if op.Value == nil {
						delete(node, leaf)
					} else {
						var kept []any
						for _, v := range node[leaf].([]any) {
							if v != *op.Value {
								kept = append(kept, v)
							}
						}
						node[leaf] = kept
					}
				} else if op.Value != nil {
					node[leaf] = *op.Value
				} else {
					node[leaf] = map[string]any{}
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": tree})
	}))
	defer api.Close()
	for _, name := range []string{"VYOS_BEARER_TOKEN", "VYOS_CA_CERTIFICATE_FILE", "VYOS_CLIENT_CERTIFICATE_FILE", "VYOS_CLIENT_KEY_FILE", "VYOS_SKIP_SAVING"} {
		t.Setenv(name, "")
	}
	config := func(entries string) string {
		return fmt.Sprintf(`
provider "vyos" {
 endpoint=%q
 auth_mode="api_key"
 api_key="isolated-test"
 insecure=true
 skip_saving=false
 commit_confirm_minutes=0
}
resource "vyos_config_batch" "test" { entries = %s }
`, api.URL, entries)
	}
	first := `{ original={path=["node","created"],value={kept="one",released="keep"}}, partial={path=["node","list"],include=["owned"]} }`
	renamed := `{ renamed={path=["node","created"],value={kept="one",released="keep"}}, renamed_partial={path=["node","list"],include=["owned"]} }`
	narrowed := `{ child={path=["node","created","kept"],value="one"}, adopted={path=["node","adopted","leaf"],value="live"}, whole_list={path=["node","list"],value=["owned","foreign"]} }`
	cleanup := `{ child={path=["node","created","kept"],value="one"}, adopted={path=["node","adopted","leaf"],value="live"}, partial_again={path=["node","list"],include=["owned"]}, remove_old={path=["node","created","released"],value=null} }`
	var owner string
	check := func(expectedWrites int, absent bool) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if writes != expectedWrites {
				return fmt.Errorf("configure calls=%d, want %d", writes, expectedWrites)
			}
			id := s.RootModule().Resources["vyos_config_batch.test"].Primary.ID
			if owner == "" {
				owner = vyos.BatchOwner(id)
			} else if vyos.BatchOwner(id) != owner {
				return fmt.Errorf("selector update changed batch owner")
			}
			node := tree["node"].(map[string]any)
			if node["adopted"].(map[string]any)["unowned"] != "keep" {
				return fmt.Errorf("adoption changed unowned sibling")
			}
			if (node["created"].(map[string]any)["released"] == nil) != absent {
				return fmt.Errorf("released subtree did not match explicit deletion intent")
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config(first), Check: check(1, false)},
			{Config: config(renamed), Check: check(1, false)},
			{Config: config(narrowed), Check: check(1, false)},
			{ResourceName: "vyos_config_batch.test", ImportState: true, ImportStateVerify: true,
				// Import has no prior native value types, so it returns the
				// supported legacy JSON representation. Verify its values below.
				ImportStateVerifyIgnore: []string{"entries"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("expected one imported batch")
					}
					for key, want := range map[string]string{
						"entries.adopted.value":    `"live"`,
						"entries.child.value":      `"one"`,
						"entries.whole_list.value": `["owned","foreign"]`,
					} {
						if states[0].Attributes[key] != want {
							return fmt.Errorf("imported configuration differs at %s", key)
						}
					}
					return nil
				}},
			{Config: config(cleanup), Check: check(2, true)},
			{PreConfig: func() {
				mu.Lock()
				defer mu.Unlock()
				tree["node"].(map[string]any)["created"].(map[string]any)["released"] = "drift"
			}, Config: config(cleanup), Check: check(3, true)},
			{Config: config(`{}`), Check: check(3, true)},
		},
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if writes != 3 || tree["node"].(map[string]any)["created"].(map[string]any)["kept"] != "one" {
				return fmt.Errorf("destroy after releasing selectors removed former configuration")
			}
			return nil
		},
	})
}
