package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func checkNativeList(values ...string) error {
	helper := os.Getenv("VYOS_NATIVE_CHECK")
	if helper == "" {
		return nil
	}
	path, _ := json.Marshal([]string{"system", "name-server"})
	want, _ := json.Marshal(values)
	output, err := exec.Command(helper, "check-list-array", string(path), string(want)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("native include preservation check failed: %s", output)
	}
	fmt.Print(string(output))
	return nil
}
func TestAccIncludeBatch(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test")
	}
	testAccPreCheck(t)
	httpClient, err := newHTTPClient(os.Getenv("VYOS_ENDPOINT"), os.Getenv("VYOS_CA_CERTIFICATE_FILE"), os.Getenv("VYOS_INSECURE") == "true")
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.CloseIdleConnections()
	client := &vyos.Client{HTTP: httpClient, Endpoint: os.Getenv("VYOS_ENDPOINT"), Key: os.Getenv("VYOS_API_KEY")}
	baseline := vyos.New(client, false, "")
	if err := baseline.Set(context.Background(), "system name-server", "192.0.2.53"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := baseline.Delete(context.Background(), "system name-server"); err != nil {
			t.Error(err)
		}
	}()
	config := func(first, second, action string) string {
		return fmt.Sprintf(`
provider "vyos" { commit_confirm_minutes = 1 }
resource "vyos_config_batch" "test" {
 entries = {
  dns_first = {path = ["system", "name-server"], include = %s}
  dns_second = {path = ["system", "name-server"], include = %s}
  policy = {
   path = ["firewall", "ipv4", "name", "TF-PROVIDER-INCLUDE"]
   value = jsonencode({"default-action" = %q})
  }
 }
}
resource "vyos_config_batch" "other" {
 entries = {
  dns = {path = ["system", "name-server"], include = toset(["192.0.2.56"])}
 }
}
`, first, second, action)
	}
	initial := config(`["192.0.2.54"]`, `["192.0.2.55"]`, "drop")
	check := func(values ...string) resource.TestCheckFunc {
		return func(_ *terraform.State) error { return checkNativeList(values...) }
	}
	externalAdded := false
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			foreign := []string{"192.0.2.53"}
			if externalAdded {
				foreign = append(foreign, "192.0.2.57")
			}
			if err := checkNativeList(foreign...); err != nil {
				return err
			}
			return testAccCheckArrayPath([]string{"firewall", "ipv4", "name", "TF-PROVIDER-INCLUDE"}, "null")
		},
		Steps: []resource.TestStep{
			{Config: initial, Check: check("192.0.2.53", "192.0.2.54", "192.0.2.55", "192.0.2.56")},
			{ResourceName: "vyos_config_batch.test", ImportState: true, ImportStateCheck: func(states []*terraform.InstanceState) error {
				if len(states) != 1 {
					return fmt.Errorf("expected one imported batch")
				}
				for key, want := range map[string]string{"entries.dns_first.include.0": "192.0.2.54", "entries.dns_second.include.0": "192.0.2.55", "entries.policy.value": `{"default-action":"drop"}`} {
					if states[0].Attributes[key] != want {
						return fmt.Errorf("imported ownership differs at %s", key)
					}
				}
				return nil
			}, ImportStateId: `{"dns_first":{"path":["system","name-server"],"include":["192.0.2.54"]},"dns_second":{"path":["system","name-server"],"include":["192.0.2.55"]},"policy":["firewall","ipv4","name","TF-PROVIDER-INCLUDE"]}`},
			{PreConfig: func() {
				external := vyos.New(client, false, "")
				desired := vyos.Batch{"external": {Path: []string{"system", "name-server"}, Partial: true, Include: []string{"192.0.2.57"}}}
				prior := vyos.Batch{"external": {Path: []string{"system", "name-server"}, Partial: true, Include: []string{"192.0.2.54"}}}
				if _, err := external.MutateOwnedBatch(context.Background(), desired, prior, "update", "external-test"); err != nil {
					t.Fatal(err)
				}
				externalAdded = true
			}, Config: initial, Check: check("192.0.2.53", "192.0.2.54", "192.0.2.55", "192.0.2.56", "192.0.2.57")},
			{Config: initial, PlanOnly: true},
			{Config: config(`["192.0.2.58"]`, `[]`, "accept"), Check: check("192.0.2.53", "192.0.2.56", "192.0.2.57", "192.0.2.58")},
			{Config: config(`[]`, `[]`, "accept"), Check: check("192.0.2.53", "192.0.2.56", "192.0.2.57")},
			{Config: config(`[]`, `[]`, "accept"), PlanOnly: true},
		},
	})
}
