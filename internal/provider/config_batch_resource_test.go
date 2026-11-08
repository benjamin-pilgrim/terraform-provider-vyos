package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccConfigBatch(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccBatchDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccBatchConfig("drop", "first", ""), Check: testAccNativeBatch()},
			{ResourceName: "vyos_config_batch.test", ImportState: true, ImportStateVerify: true},
			{Config: testAccBatchConfig("accept", "first", ""), Check: testAccNativeBatch()},
			{Config: testAccBatchConfig("accept", "first", ""), PlanOnly: true},
			// A changed entry name/path set is explicitly replaced, rather than
			// silently dropping ownership of the original paths.
			{Config: testAccBatchConfig("drop", "renamed", ""), Check: testAccNativeBatch()},
		},
	})
}

func TestAccConfigBatchSaveFailureRecovery(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccBatchDestroyed,
		Steps: []resource.TestStep{
			{Config: testAccBatchConfig("drop", "first", `provider "vyos" { save_file = "/proc/provider-batch-test.boot" }`), ExpectError: regexp.MustCompile("saving failed")},
			// Without the failed create's identity, this would try to create
			// over the already active paths and fail the ownership preflight.
			{Config: testAccBatchConfig("accept", "first", ""), Check: testAccNativeBatch()},
		},
	})
}

func testAccBatchConfig(action, name, provider string) string {
	return fmt.Sprintf(`
%s
resource "vyos_config_batch" "test" {
  entries = {
    %s = {
      path = ["firewall", "ipv4", "name", "TF-PROVIDER-BATCH-A"]
      value = jsonencode({ "default-action" = %q })
    }
    second = {
      path = ["firewall", "ipv4", "name", "TF-PROVIDER-BATCH-B"]
      value = jsonencode({ "default-action" = "accept" })
    }
  }
}
`, provider, name, action)
}

func testAccNativeBatch() resource.TestCheckFunc {
	return func(state *terraform.State) error {
		if os.Getenv("VYOS_NATIVE_CHECK") == "" {
			return nil
		}
		rs := state.RootModule().Resources["vyos_config_batch.test"]
		if rs == nil || rs.Primary == nil {
			return fmt.Errorf("missing batch resource state")
		}
		entries, err := vyos.ParseBatchIdentity(rs.Primary.ID)
		if err != nil {
			return err
		}
		for name, entry := range entries {
			if err := testAccCheckArrayPath(entry.Path, rs.Primary.Attributes["entries."+name+".value"]); err != nil {
				return err
			}
		}
		return nil
	}
}

func testAccCheckArrayPath(path []string, value string) error {
	helper := os.Getenv("VYOS_NATIVE_CHECK")
	if helper == "" {
		return nil
	}
	raw, _ := json.Marshal(path)
	output, err := exec.Command(helper, "check-array", string(raw), value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("native batch persistence check failed: %s", output)
	}
	fmt.Print(string(output))
	return nil
}

func testAccBatchDestroyed(state *terraform.State) error {
	for _, name := range []string{"TF-PROVIDER-BATCH-A", "TF-PROVIDER-BATCH-B"} {
		if err := testAccCheckArrayPath([]string{"firewall", "ipv4", "name", name}, "null"); err != nil {
			return err
		}
	}
	return nil
}
