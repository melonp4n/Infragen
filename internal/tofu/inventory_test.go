package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// ansibleHost is one Ansible-managed asset reachable on SSH, with or without a
// public address — which is the difference the inventory has to express.
func ansibleHost(provider, code string, public bool) model.Session {
	s := inboundChart(provider, code)
	s.Connections[0].AToB = []model.Rule{{Protocol: "TCP", Port: "22"}}
	a := &s.Accounts[0].Assets[0]
	a.Params = map[string]any{
		catalog.ParamAnsible:      true,
		catalog.ParamAnsibleGroup: "c2",
	}
	if public {
		a.Params[catalog.ParamStaticPublicIP] = true
		a.Params["associate_public_ip_address"] = true
	}
	return s
}

// Ansible may be run from outside the network or inside it, and the inventory
// cannot know which. It names the public address and carries the private one
// commented beneath, so switching is uncommenting a line rather than looking an
// address up.
func TestInventoryCarriesBothAddresses(t *testing.T) {
	for _, tc := range []struct{ provider, code, public, private string }{
		{"aws", "EC2", "aws_eip.asset_a.public_ip", "aws_instance.asset_a.private_ip"},
		{"gcp", "GCE", "google_compute_address.asset_a.address", "google_compute_instance.asset_a.network_interface[0].network_ip"},
		{"azure", "VM", "azurerm_public_ip.asset_a.ip_address", "azurerm_linux_virtual_machine.asset_a.private_ip_address"},
		{"digitalocean", "DRP", "digitalocean_droplet.asset_a.ipv4_address", "digitalocean_droplet.asset_a.ipv4_address_private"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			hcl, _ := generate(t, ansibleHost(tc.provider, tc.code, true))
			want := []string{
				"${" + tc.public + "} ansible_user=",
				"# ${" + tc.private + "} ansible_user=",
			}
			for _, w := range want {
				if !strings.Contains(hcl, w) {
					t.Errorf("inventory does not contain %q:\n%s", w, hcl)
				}
			}
		})
	}
}

// A host with no public address is still worth an inventory entry — it is
// reachable from a jump box, just not from wherever infrachart is running. It
// used to be left out entirely.
func TestPrivateOnlyHostIsStillInTheInventory(t *testing.T) {
	hcl, warnings := generate(t, ansibleHost("gcp", "GCE", false))

	const private = "${google_compute_instance.asset_a.network_interface[0].network_ip} ansible_user="
	if !strings.Contains(hcl, "\n"+strings.TrimSuffix(private, " ansible_user=")) &&
		!strings.Contains(hcl, private) {
		t.Errorf("a private-only host produced no inventory line:\n%s", hcl)
	}
	// Uncommented: it is the address Ansible will use, not an alternative to one.
	if strings.Contains(hcl, "# "+private) {
		t.Error("the only address available was commented out")
	}
	if !strings.Contains(hcl, "(private address only)") {
		t.Error("nothing in the inventory says the address is a private one")
	}

	var said bool
	for _, w := range warnings {
		if strings.Contains(w, "private address instead") {
			said = true
		}
	}
	if !said {
		t.Errorf("no warning explained the fallback: %v", warnings)
	}
}
