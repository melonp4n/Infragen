package tofu

import (
	"fmt"

	"infrachart/internal/model"
)

// Azure attaches rules to a network security group and orders them by priority.
// Two rules sharing a priority is an apply error, so priorities are allocated
// deterministically from position rather than chosen.
type azureFirewall struct{ ruleList }

func (f *azureFirewall) render(w *writer, acc model.Account) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		// Azure attaches a security group to a network interface, never to a
		// resource directly, so a type with no NIC companion has nothing to attach
		// to. Emitting the group anyway produced an association naming a NIC that
		// was never declared, which failed at validate.
		nic := firewallRef(acc, asset)
		if nic == "" {
			continue
		}
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_network_security_group", asset), func() {
			w.arg("provider", "azurerm."+acc.ID)
			w.arg("name", quote(asset))
			w.arg("location", fmt.Sprintf("azurerm_resource_group.%s.location", acc.ID))
			w.arg("resource_group_name", fmt.Sprintf("azurerm_resource_group.%s.name", acc.ID))
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_network_interface_security_group_association", asset), func() {
			w.arg("provider", "azurerm."+acc.ID)
			w.arg("network_interface_id", nic)
			w.arg("network_security_group_id", fmt.Sprintf("azurerm_network_security_group.%s.id", asset))
		})
		for i, r := range grouped[asset] {
			f.renderRule(w, acc.ID, r, i)
		}
	}
}

func (f *azureFirewall) renderRule(w *writer, accountID string, r fwRule, index int) {
	direction, peerKey, otherKey := "Inbound", "source_address_prefix", "destination_address_prefix"
	if r.Dir == dirEgress {
		direction, peerKey, otherKey = "Outbound", "destination_address_prefix", "source_address_prefix"
	}
	w.blank()
	w.line("# %s", r.Comment)
	w.block(fmt.Sprintf("resource %q %q", "azurerm_network_security_rule", ruleName(r, index)), func() {
		w.arg("provider", "azurerm."+accountID)
		w.arg("name", quote(ruleName(r, index)))
		// Deterministic from position: Azure rejects duplicate priorities, and a
		// regenerated chart must produce the same numbers.
		w.arg("priority", fmt.Sprint(100+index))
		w.arg("direction", quote(direction))
		w.arg("access", quote("Allow"))
		w.arg("protocol", quote(azureProto(r.Proto)))
		w.arg("source_port_range", quote("*"))
		w.arg("destination_port_range", quote(azurePorts(r)))
		w.arg(peerKey, interp(azurePeer(r)))
		w.arg(otherKey, quote("*"))
		w.arg("resource_group_name", fmt.Sprintf("azurerm_resource_group.%s.name", accountID))
		w.arg("network_security_group_name", fmt.Sprintf("azurerm_network_security_group.%s.name", r.Asset))
	})
}

// azureProto is the one provider that does not lowercase: it wants Tcp, Udp, Icmp
// and * for everything.
func azureProto(p string) string {
	switch p {
	case "TCP":
		return "Tcp"
	case "UDP":
		return "Udp"
	case "ICMP":
		return "Icmp"
	}
	return "*"
}

func azurePorts(r fwRule) string {
	if r.Proto == "ALL" {
		return "*"
	}
	return ports(r, "*")
}

// azurePeer maps a peer to an address prefix. Azure has no group reference, so a
// same-account pair falls back to the peer's private address.
func azurePeer(r fwRule) string {
	switch r.Kind {
	case peerServiceTag:
		return r.Peer
	case peerSecurityGroup:
		return fmt.Sprintf("${azurerm_linux_virtual_machine.%s.private_ip_address}/32", r.Peer)
	default:
		return r.Peer
	}
}
