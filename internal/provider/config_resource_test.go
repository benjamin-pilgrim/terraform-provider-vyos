package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccConfigResourceSimpleFirewallRuleset(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: testAccConfigResourceConfig("firewall ipv4 name TEST", `jsonencode({default-action = "drop"})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vyos_config.test", "id", "firewall ipv4 name TEST"),
					resource.TestCheckResourceAttr("vyos_config.test", "path", "firewall ipv4 name TEST"),
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
				Config: testAccConfigResourceConfig("firewall ipv4 name TEST", `jsonencode({"default-action" = "accept"})`),
				Check: resource.ComposeAggregateTestCheckFunc(
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
				Config: testAccConfigResourceConfig("firewall ipv4 name TEST",
					`jsonencode({
								default-action = "drop"
								rule = {
									10 = {
										action = "accept"
									}
								}
							})`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("vyos_config.test", "id", "firewall ipv4 name TEST"),
					resource.TestCheckResourceAttr("vyos_config.test", "path", "firewall ipv4 name TEST"),
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
				Config: testAccConfigResourceConfig("firewall ipv4 name TEST", `jsonencode({"default-action" = "accept"})`),
				Check: resource.ComposeAggregateTestCheckFunc(
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
