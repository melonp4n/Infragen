package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/model"
)

// inboundChart is one account whose every asset is reachable from the internet,
// which is the shortest way to make each provider's firewall emitter run.
func inboundChart(provider string, codes ...string) model.Session {
	s := model.Session{
		Version:  model.SchemaVersion,
		Internet: model.Point{X: 1020, Y: 40},
		Accounts: []model.Account{{ID: "acc_1", Name: "Account", Provider: provider}},
	}
	for i, code := range codes {
		id := "asset_" + string(rune('a'+i))
		s.Accounts[0].Assets = append(s.Accounts[0].Assets, model.Asset{ID: id, Code: code, Name: code})
		s.Connections = append(s.Connections, model.Connection{
			ID:   "conn_" + id,
			A:    model.NodeRef{Type: model.NodeInternet},
			B:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc_1", AssetID: id},
			AToB: []model.Rule{{Protocol: "TCP", Port: "443"}},
		})
	}
	return s
}

// A firewall that names a resource the type does not have is worse than no
// firewall: it fails at validate, and before that it reads as protection. Azure
// attaches a security group to a network interface, and only a VM has one.
func TestUnattachableTypesGetNoFirewall(t *testing.T) {
	for _, tc := range []struct{ provider, code, absent string }{
		{"azure", "SQL", "azurerm_network_security_group"},
		{"azure", "AKS", "azurerm_network_security_group"},
		{"digitalocean", "DB", "digitalocean_firewall"},
		{"digitalocean", "LB", "digitalocean_firewall"},
	} {
		t.Run(tc.provider+"/"+tc.code, func(t *testing.T) {
			hcl, _ := generate(t, inboundChart(tc.provider, tc.code))
			if strings.Contains(hcl, tc.absent) {
				t.Errorf("a %s emitted a %s, which has nothing to attach to:\n%s", tc.code, tc.absent, hcl)
			}
			// Silence would be the worse failure: the chart would look protected.
			if !strings.Contains(hcl, "attach its firewall yourself") {
				t.Error("nothing reported that the asset is unprotected")
			}
		})
	}
}

// The types that can be wired still are, or the fix above would have turned every
// firewall off.
func TestAttachableTypesStillGetTheirFirewall(t *testing.T) {
	for _, tc := range []struct{ provider, code, want string }{
		{"azure", "VM", "azurerm_network_interface_security_group_association"},
		{"digitalocean", "DRP", "digitalocean_firewall"},
	} {
		t.Run(tc.provider+"/"+tc.code, func(t *testing.T) {
			hcl, _ := generate(t, inboundChart(tc.provider, tc.code))
			if !strings.Contains(hcl, tc.want) {
				t.Errorf("a %s did not get its %s:\n%s", tc.code, tc.want, hcl)
			}
			if strings.Contains(hcl, "attach its firewall yourself") {
				t.Error("an attached asset was reported as unattached")
			}
		})
	}
}

// DigitalOcean has no "every protocol" value — a digitalocean_firewall rule takes
// tcp, udp or icmp. Emitting protocol = "all" was rejected at apply, and validate
// never saw it because the provider checks the value rather than the schema.
func TestAllProtocolBecomesThreeDigitalOceanRules(t *testing.T) {
	s := inboundChart("digitalocean", "DRP")
	s.Connections[0].AToB = []model.Rule{{Protocol: "ALL", Port: "*"}}

	hcl, _ := generate(t, s)
	if strings.Contains(hcl, `protocol = "all"`) {
		t.Error(`emitted protocol = "all", which DigitalOcean rejects`)
	}
	for _, want := range []string{`protocol = "tcp"`, `protocol = "udp"`, `protocol = "icmp"`} {
		if !strings.Contains(collapse(hcl), want) {
			t.Errorf("an ALL rule did not produce %s:\n%s", want, hcl)
		}
	}
}
