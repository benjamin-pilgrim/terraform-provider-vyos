package provider

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccConfigResourceSimpleFirewallRuleset(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: testAccConfigResourceConfig("firewall ipv4 name TF-PROVIDER-TEST", `jsonencode({default-action = "drop"})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccNativeConfig(),
					resource.TestCheckResourceAttr("vyos_config.test", "id", "firewall ipv4 name TF-PROVIDER-TEST"),
					resource.TestCheckResourceAttr("vyos_config.test", "path", "firewall ipv4 name TF-PROVIDER-TEST"),
					resource.TestCheckResourceAttr("vyos_config.test", "value", `{"default-action":"drop"}`),
				),
			},
			// ImportState testing
			{
				ResourceName:            "vyos_config.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{},
			},
			// Update and Read testing
			{
				Config: testAccConfigResourceConfig("firewall ipv4 name TF-PROVIDER-TEST", `jsonencode({"default-action" = "accept"})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccNativeConfig(),
					resource.TestCheckResourceAttr("vyos_config.test", "value", `{"default-action":"accept"}`),
				),
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccConfigResourceComplexFirewallRuleset(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: testAccConfigResourceConfig("firewall ipv4 name TF-PROVIDER-TEST",
					`jsonencode({
								default-action = "drop"
								rule = {
									10 = {
										action = "accept"
									}
								}
							})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccNativeConfig(),
					resource.TestCheckResourceAttr("vyos_config.test", "id", "firewall ipv4 name TF-PROVIDER-TEST"),
					resource.TestCheckResourceAttr("vyos_config.test", "path", "firewall ipv4 name TF-PROVIDER-TEST"),
					resource.TestCheckResourceAttr("vyos_config.test", "value", `{"default-action":"drop","rule":{"10":{"action":"accept"}}}`),
				),
			},
			// ImportState testing
			{
				ResourceName:            "vyos_config.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{},
			},
			// Update and Read testing
			{
				Config: testAccConfigResourceConfig("firewall ipv4 name TF-PROVIDER-TEST", `jsonencode({"default-action" = "accept"})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccNativeConfig(),
					resource.TestCheckResourceAttr("vyos_config.test", "value", `{"default-action":"accept"}`),
				),
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccConfigResourceBinaryOption(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: testAccConfigResourceConfig("service ssh disable-host-validation", `jsonencode({})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccNativeConfig(),
					resource.TestCheckResourceAttr("vyos_config.test", "id", "service ssh disable-host-validation"),
					resource.TestCheckResourceAttr("vyos_config.test", "path", "service ssh disable-host-validation"),
					resource.TestCheckResourceAttr("vyos_config.test", "value", `{}`),
				),
			},
			// ImportState testing
			{
				ResourceName:            "vyos_config.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{},
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func testAccConfigResourceConfig(path string, value string) string {
	return fmt.Sprintf(`
resource "vyos_config" "test" {
  path = %[1]q
  value = %[2]v
}
`, path, value)
}

// The disposable CI VM supplies a serial-console checker, independently checking
// active and saved configuration after each create/update. External endpoints
// can still run the API acceptance suite without this VM-specific adapter.
func testAccNativeConfig() resource.TestCheckFunc {
	return func(state *terraform.State) error {
		helper := os.Getenv("VYOS_NATIVE_CHECK")
		if helper == "" {
			return nil
		}
		rs, ok := state.RootModule().Resources["vyos_config.test"]
		if !ok || rs.Primary == nil {
			return fmt.Errorf("missing resource for native persistence check")
		}
		output, err := exec.Command(helper, "check", rs.Primary.Attributes["path"], rs.Primary.Attributes["value"]).CombinedOutput()
		if err != nil {
			return fmt.Errorf("native active/saved check failed: %s", output)
		}
		fmt.Print(string(output))
		return nil
	}
}
