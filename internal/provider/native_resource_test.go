package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccNativeValues(t *testing.T) {
	config := func(action string) string {
		return fmt.Sprintf(`
provider "vyos" {}
resource "terraform_data" "action" { input = %q }
resource "vyos_config" "test" {
 path = "firewall ipv4 name TF-PROVIDER-TEST"
 value = {
  "default-action" = terraform_data.action.output
  description = null
  rule = { "10" = {action = "accept", protocol = "tcp", destination = {port = "443"}} }
 }
}
`, action)
	}
	check := func(action string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			return testAccCheckArrayPath([]string{"firewall", "ipv4", "name", "TF-PROVIDER-TEST"}, fmt.Sprintf(`{"default-action":%q,"rule":{"10":{"action":"accept","protocol":"tcp","destination":{"port":"443"}}}}`, action))
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("drop"), Check: check("drop")},
			{Config: config("drop"), PlanOnly: true},
			{Config: config("accept"), Check: check("accept")},
			{Config: config("accept"), PlanOnly: true},
		},
	})
}

func TestAccNativeScalarListAndDefaultFlag(t *testing.T) {
	config := func(value string) string {
		return fmt.Sprintf(`
provider "vyos" {}
resource "vyos_config" "test" {
 path = "system name-server"
 value = %s
}
resource "vyos_config" "flag" {
 path = "service ssh disable-host-validation"
}
`, value)
	}
	check := func(value string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			if err := testAccCheckArrayPath([]string{"system", "name-server"}, value); err != nil {
				return err
			}
			return testAccCheckArrayPath([]string{"service", "ssh", "disable-host-validation"}, `{}`)
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if err := testAccCheckArrayPath([]string{"system", "name-server"}, "null"); err != nil {
				return err
			}
			return testAccCheckArrayPath([]string{"service", "ssh", "disable-host-validation"}, "null")
		},
		Steps: []resource.TestStep{
			{Config: config(`"192.0.2.53"`), Check: check(`"192.0.2.53"`)},
			{Config: config(`"192.0.2.53"`), PlanOnly: true},
			{Config: config(`["192.0.2.53","192.0.2.54"]`), Check: check(`["192.0.2.53","192.0.2.54"]`)},
			{Config: config(`["192.0.2.53","192.0.2.54"]`), PlanOnly: true},
		},
	})
}

func TestAccNativeBatchValues(t *testing.T) {
	config := func(action string) string {
		return fmt.Sprintf(`
provider "vyos" {}
resource "terraform_data" "action" { input = %q }
resource "vyos_config_batch" "test" {
 entries = {
  first = {
   path = ["firewall", "ipv4", "name", "TF-PROVIDER-BATCH-A"]
   value = {"default-action" = terraform_data.action.output, description = null}
  }
  second = {
   path = ["firewall", "ipv4", "name", "TF-PROVIDER-BATCH-B"]
   value = jsonencode({"default-action" = "accept"})
  }
 }
}
`, action)
	}
	check := func(action string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			if err := testAccCheckArrayPath([]string{"firewall", "ipv4", "name", "TF-PROVIDER-BATCH-A"}, fmt.Sprintf(`{"default-action":%q}`, action)); err != nil {
				return err
			}
			return testAccCheckArrayPath([]string{"firewall", "ipv4", "name", "TF-PROVIDER-BATCH-B"}, `{"default-action":"accept"}`)
		}
	}
	resource.Test(t, resource.TestCase{PreCheck: func() { testAccPreCheck(t) }, ProtoV6ProviderFactories: testAccProtoV6ProviderFactories, CheckDestroy: testAccBatchDestroyed,
		Steps: []resource.TestStep{
			{Config: config("drop"), Check: check("drop")},
			{Config: config("drop"), PlanOnly: true},
			{Config: config("accept"), Check: check("accept")},
			{Config: config("accept"), PlanOnly: true},
		},
	})
}
