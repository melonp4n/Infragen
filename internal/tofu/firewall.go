package tofu

import (
	"fmt"
	"sort"
	"strings"

	"infrachart/internal/catalog"
)

// Rule direction, from the point of view of the asset the rule attaches to.
const (
	dirIngress = "ingress"
	dirEgress  = "egress"
)

// What a rule's source or destination actually is. The providers disagree about
// which of these they support, which is why it is carried explicitly rather than
// flattened into a string.
type peerKind int

const (
	peerCIDR          peerKind = iota
	peerSecurityGroup          // Peer is an asset ID whose group is referenced
	peerPrefixList             // Peer is a managed prefix list name
	peerServiceTag             // Peer is a provider service tag
)

// fwRule is one directional firewall rule resolved to concrete values, ready for
// a provider to render however its own model requires.
type fwRule struct {
	Asset   string // asset ID the rule attaches to; also its resource name
	Dir     string
	Proto   string // TCP | UDP | ICMP | ALL
	Lo, Hi  int
	AnyPort bool
	Peer    string
	Kind    peerKind
	Comment string // "from X" / "to X", plus the rule's note
}

// firewall accumulates an account's rules and renders them at the end.
//
// The accumulate-then-render shape is forced by DigitalOcean: its inbound and
// outbound lists live inside a single digitalocean_firewall resource, so a
// one-block-per-rule interface could not express it.
type firewall interface {
	add(fwRule)
	render(w *writer, accountID string)
}

func newFirewall(provider string) firewall {
	switch provider {
	case "aws":
		return &awsFirewall{}
	case "azure":
		return &azureFirewall{}
	case "gcp":
		return &gcpFirewall{}
	case "digitalocean":
		return &doFirewall{}
	}
	return nil
}

// byAsset groups accumulated rules by the asset they protect, in asset-ID order so
// output is deterministic.
func byAsset(rules []fwRule) ([]string, map[string][]fwRule) {
	grouped := map[string][]fwRule{}
	for _, r := range rules {
		grouped[r.Asset] = append(grouped[r.Asset], r)
	}
	assets := make([]string, 0, len(grouped))
	for a := range grouped {
		assets = append(assets, a)
	}
	sort.Strings(assets)
	return assets, grouped
}

// ruleName builds a stable, unique resource name for one rule. It is derived from
// the asset ID and the rule's own shape, never from a display name — see the
// rename-stability rule in documentation/TOFU-MAPPING.md.
func ruleName(r fwRule, index int) string {
	port := "any"
	if !r.AnyPort {
		port = fmt.Sprintf("%d_%d", r.Lo, r.Hi)
	}
	return fmt.Sprintf("%s_%s_%s_%s_%d", r.Asset, r.Dir, strings.ToLower(r.Proto), port, index)
}

// ---- AWS -------------------------------------------------------------------

// AWS is the straightforward case: one security group per asset, and one rule
// resource per rule. A source can be another security group, which is the only
// representation that cannot go stale.
type awsFirewall struct{ rules []fwRule }

func (f *awsFirewall) add(r fwRule) { f.rules = append(f.rules, r) }

func (f *awsFirewall) render(w *writer, accountID string) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_security_group", asset), func() {
			w.arg("provider", "aws."+accountID)
			w.arg("name", quote(asset))
			w.arg("vpc_id", fmt.Sprintf("aws_vpc.%s.id", accountID))
		})
		for i, r := range grouped[asset] {
			f.renderRule(w, accountID, r, i)
		}
	}
}

func (f *awsFirewall) renderRule(w *writer, accountID string, r fwRule, index int) {
	resource := "aws_vpc_security_group_ingress_rule"
	peerArg := "cidr_ipv4"
	if r.Dir == dirEgress {
		resource = "aws_vpc_security_group_egress_rule"
	}
	w.blank()
	w.line("# %s", r.Comment)
	w.block(fmt.Sprintf("resource %q %q", resource, ruleName(r, index)), func() {
		w.arg("provider", "aws."+accountID)
		w.arg("security_group_id", fmt.Sprintf("aws_security_group.%s.id", r.Asset))
		w.arg("ip_protocol", quote(awsProto(r.Proto)))
		// TCP and UDP rules must carry a port range even when the rule covers all
		// ports: AWS rejects a tcp rule with no ports at apply time, and `validate`
		// does not catch it because the schema marks them optional.
		if r.Proto == "TCP" || r.Proto == "UDP" {
			lo, hi := r.Lo, r.Hi
			if r.AnyPort {
				lo, hi = 0, 65535
			}
			w.arg("from_port", fmt.Sprint(lo))
			w.arg("to_port", fmt.Sprint(hi))
		}
		switch r.Kind {
		case peerSecurityGroup:
			w.arg("referenced_security_group_id", fmt.Sprintf("aws_security_group.%s.id", r.Peer))
		case peerPrefixList:
			w.arg("prefix_list_id", fmt.Sprintf("data.aws_ec2_managed_prefix_list.%s.id", prefixListName(r.Peer)))
		default:
			w.arg(peerArg, interp(r.Peer))
		}
	})
}

func awsProto(p string) string {
	if p == "ALL" {
		return "-1"
	}
	return strings.ToLower(p)
}

// prefixListName turns a managed prefix list name into an HCL-safe data source
// name, e.g. com.amazonaws.global.cloudfront.origin-facing.
func prefixListName(list string) string {
	r := strings.NewReplacer(".", "_", "-", "_")
	return r.Replace(list)
}

// ---- Azure -----------------------------------------------------------------

// Azure attaches rules to a network security group and orders them by priority.
// Two rules sharing a priority is an apply error, so priorities are allocated
// deterministically from position rather than chosen.
type azureFirewall struct{ rules []fwRule }

func (f *azureFirewall) add(r fwRule) { f.rules = append(f.rules, r) }

func (f *azureFirewall) render(w *writer, accountID string) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_network_security_group", asset), func() {
			w.arg("provider", "azurerm."+accountID)
			w.arg("name", quote(asset))
			w.arg("location", fmt.Sprintf("azurerm_resource_group.%s.location", accountID))
			w.arg("resource_group_name", fmt.Sprintf("azurerm_resource_group.%s.name", accountID))
		})
		// The network interface companion is what makes this possible: Azure
		// attaches a security group to a NIC or a subnet, never to a VM directly.
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_network_interface_security_group_association", asset), func() {
			w.arg("provider", "azurerm."+accountID)
			w.arg("network_interface_id", fmt.Sprintf("azurerm_network_interface.%s_nic.id", asset))
			w.arg("network_security_group_id", fmt.Sprintf("azurerm_network_security_group.%s.id", asset))
		})
		for i, r := range grouped[asset] {
			f.renderRule(w, accountID, r, i)
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

func azureProto(p string) string {
	switch p {
	case "ALL":
		return "*"
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
	if r.AnyPort || r.Proto == "ALL" {
		return "*"
	}
	if r.Lo == r.Hi {
		return fmt.Sprint(r.Lo)
	}
	return fmt.Sprintf("%d-%d", r.Lo, r.Hi)
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

// ---- GCP -------------------------------------------------------------------

// GCP firewall rules live on the network and select instances by tag, so there is
// no per-asset group. Each asset gets a generated tag.
type gcpFirewall struct{ rules []fwRule }

func (f *gcpFirewall) add(r fwRule) { f.rules = append(f.rules, r) }

func (f *gcpFirewall) render(w *writer, accountID string) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		for i, r := range grouped[asset] {
			w.blank()
			w.line("# %s", r.Comment)
			w.block(fmt.Sprintf("resource %q %q", "google_compute_firewall", ruleName(r, i)), func() {
				w.arg("provider", "google."+accountID)
				w.arg("name", quote(strings.ReplaceAll(ruleName(r, i), "_", "-")))
				w.arg("network", fmt.Sprintf("google_compute_network.%s.name", accountID))
				w.arg("direction", quote(strings.ToUpper(r.Dir)))
				w.block("allow", func() {
					w.arg("protocol", quote(gcpProto(r.Proto)))
					if !r.AnyPort && r.Proto != "ALL" {
						w.arg("ports", fmt.Sprintf("[%s]", quote(gcpPorts(r))))
					}
				})
				// Tags select which instances the rule applies to. Ranges are the
				// only peer representation GCP offers here.
				w.arg("target_tags", fmt.Sprintf("[%s]", quote(gcpTag(r.Asset))))
				key := "source_ranges"
				if r.Dir == dirEgress {
					key = "destination_ranges"
				}
				w.arg(key, gcpRanges(r))
			})
		}
	}
}

func gcpProto(p string) string {
	if p == "ALL" {
		return "all"
	}
	return strings.ToLower(p)
}

func gcpPorts(r fwRule) string {
	if r.Lo == r.Hi {
		return fmt.Sprint(r.Lo)
	}
	return fmt.Sprintf("%d-%d", r.Lo, r.Hi)
}

// gcpTag is the network tag for an asset. Tags must be lowercase and use hyphens.
func gcpTag(assetID string) string {
	return strings.ReplaceAll(strings.ToLower(assetID), "_", "-")
}

// gcpRanges renders the peer as a list of CIDRs. A same-account pair uses the
// peer's tag instead, which GCP supports for source but not destination.
func gcpRanges(r fwRule) string {
	if r.Kind == peerSecurityGroup {
		return fmt.Sprintf("[%s]", quote("10.0.0.0/8"))
	}
	parts := strings.Split(strings.TrimPrefix(r.Peer, "cidr:"), ",")
	for i, p := range parts {
		parts[i] = interp(strings.TrimSpace(p))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// ---- DigitalOcean ----------------------------------------------------------

// DigitalOcean puts inbound and outbound lists inside one firewall resource, so
// its rules cannot be emitted one block at a time. This is the case the
// accumulate-then-render interface exists for.
type doFirewall struct{ rules []fwRule }

func (f *doFirewall) add(r fwRule) { f.rules = append(f.rules, r) }

func (f *doFirewall) render(w *writer, accountID string) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "digitalocean_firewall", asset), func() {
			w.arg("name", quote(strings.ReplaceAll(asset, "_", "-")))
			w.arg("droplet_ids", fmt.Sprintf("[digitalocean_droplet.%s.id]", asset))
			for _, r := range grouped[asset] {
				blockName, peerKey := "inbound_rule", "source_addresses"
				if r.Dir == dirEgress {
					blockName, peerKey = "outbound_rule", "destination_addresses"
				}
				w.blank()
				w.line("# %s", r.Comment)
				w.block(blockName, func() {
					w.arg("protocol", quote(strings.ToLower(r.Proto)))
					w.arg("port_range", quote(doPorts(r)))
					w.arg(peerKey, fmt.Sprintf("[%s]", interp(doPeer(r))))
				})
			}
		})
	}
}

func doPorts(r fwRule) string {
	if r.AnyPort || r.Proto == "ALL" || r.Proto == "ICMP" {
		return "all"
	}
	if r.Lo == r.Hi {
		return fmt.Sprint(r.Lo)
	}
	return fmt.Sprintf("%d-%d", r.Lo, r.Hi)
}

// doPeer maps a peer to an address. DigitalOcean firewalls can reference droplets
// by ID, which is what a same-account pair uses.
func doPeer(r fwRule) string {
	if r.Kind == peerSecurityGroup {
		return fmt.Sprintf("${digitalocean_droplet.%s.ipv4_address}/32", r.Peer)
	}
	return strings.TrimPrefix(r.Peer, "cidr:")
}

// ---- turning outcomes into rules -------------------------------------------

// fwRulesFor converts one classified outcome into the firewall rules it implies:
// an ingress rule on the destination, an egress rule on the source, or both.
//
// Only firewalled endpoints get rules. Everything else was already classified as
// IAM, public-by-design or refused, and produces nothing here.
func fwRulesFor(f Flow, o Outcome) []fwRule {
	if o.Strategy == StratRefused || o.Strategy == StratIAM ||
		o.Strategy == StratPublicByDesign || o.Strategy == StratEdgeForeign {
		return nil
	}

	lo, hi, anyPort := o.Rule.Ports()
	base := fwRule{Proto: o.Rule.Protocol, Lo: lo, Hi: hi, AnyPort: anyPort}
	note := ""
	if o.Rule.Detail != "" {
		note = " — " + o.Rule.Detail
	}

	kind := peerCIDR
	switch o.Strategy {
	case StratSecurityGroupRef:
		kind = peerSecurityGroup
	case StratEdgeNative:
		kind = edgeKind(o.Source)
	}
	peer := strings.TrimPrefix(strings.TrimPrefix(o.Source,
		"aws_ec2_managed_prefix_list:"), "service_tag:")

	var out []fwRule

	// Ingress on the destination, sourced from the peer.
	if f.To.Network() == catalog.NetFirewalled && f.To.Asset != nil {
		r := base
		r.Asset, r.Dir, r.Kind, r.Peer = f.To.Asset.ID, dirIngress, kind, peer
		if kind == peerSecurityGroup && f.From.Asset != nil {
			r.Peer = f.From.Asset.ID
		}
		r.Comment = "from " + f.From.Label + note
		out = append(out, r)
	}

	// Egress on the source, towards the destination. A CDN has no firewall of its
	// own, so an edge strategy produces ingress only.
	if kind != peerPrefixList && kind != peerServiceTag &&
		f.From.Network() == catalog.NetFirewalled && f.From.Asset != nil {
		r := base
		r.Asset, r.Dir, r.Kind = f.From.Asset.ID, dirEgress, kind
		r.Peer = destPeer(f, o, kind)
		r.Comment = "to " + f.To.Label + note
		out = append(out, r)
	}
	return out
}

// destPeer is the address an egress rule allows traffic towards.
func destPeer(f Flow, o Outcome, kind peerKind) string {
	if kind == peerSecurityGroup && f.To.Asset != nil {
		return f.To.Asset.ID
	}
	// For egress the classifier already resolved the destination literal.
	return o.Source
}

// edgeKind maps an edge construct to the peer kind that renders it.
func edgeKind(source string) peerKind {
	switch {
	case strings.HasPrefix(source, "aws_ec2_managed_prefix_list:"):
		return peerPrefixList
	case strings.HasPrefix(source, "service_tag:"):
		return peerServiceTag
	default:
		return peerCIDR
	}
}

// prefixLists collects the managed prefix lists a chart needs, so each is
// declared as a data source exactly once.
func prefixLists(r Report) []string {
	seen := map[string]bool{}
	for _, f := range r.Flows {
		for _, o := range f.Outcomes {
			if o.Strategy == StratEdgeNative && edgeKind(o.Source) == peerPrefixList {
				seen[strings.TrimPrefix(o.Source, "aws_ec2_managed_prefix_list:")] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
