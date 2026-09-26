package tofu

import (
	"fmt"

	"infrachart/internal/model"
)

// DigitalOcean puts inbound and outbound lists inside one firewall resource, so
// its rules cannot be emitted one block at a time. This is the case the
// accumulate-then-render interface exists for.
type doFirewall struct{ ruleList }

func (f *doFirewall) render(w *writer, acc model.Account) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		// A digitalocean_firewall attaches to droplets and nothing else, so a
		// managed database, Kubernetes cluster or load balancer has nothing to
		// point at. The previous version named a droplet resource that did not
		// exist, which failed at validate.
		droplet := firewallRef(acc, asset)
		if droplet == "" {
			continue
		}
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "digitalocean_firewall", asset), func() {
			w.arg("name", quote(dashed(asset)))
			w.arg("droplet_ids", "["+droplet+"]")
			for _, r := range grouped[asset] {
				blockName, peerKey := "inbound_rule", "source_addresses"
				if r.Dir == dirEgress {
					blockName, peerKey = "outbound_rule", "destination_addresses"
				}
				// DigitalOcean has no "every protocol" value, so an ALL rule is the
				// three it does accept. Spelling it "all" produced a protocol the
				// provider rejects.
				for _, proto := range doProtocols(r) {
					w.blank()
					w.line("# %s", r.Comment)
					w.block(blockName, func() {
						w.arg("protocol", quote(proto))
						w.arg("port_range", quote(doPorts(r)))
						w.arg(peerKey, fmt.Sprintf("[%s]", interp(doPeer(r))))
					})
				}
			}
		})
	}
}

// doProtocols is the protocols one rule becomes. Everything but ALL is itself.
func doProtocols(r fwRule) []string {
	if r.Proto == "ALL" {
		return []string{"tcp", "udp", "icmp"}
	}
	return []string{lowerProto(r.Proto, "")}
}

func doPorts(r fwRule) string {
	if r.Proto == "ALL" || r.Proto == "ICMP" {
		return "all"
	}
	return ports(r, "all")
}

// doPeer maps a peer to an address. DigitalOcean firewalls can reference droplets
// by ID, which is what a same-account pair uses.
func doPeer(r fwRule) string {
	if r.Kind == peerSecurityGroup {
		return fmt.Sprintf("${digitalocean_droplet.%s.ipv4_address}/32", r.Peer)
	}
	return r.Peer
}
