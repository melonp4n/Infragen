package tofu

import (
	"fmt"
	"sort"
	"strings"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
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
	render(w *writer, acc model.Account)
}

// ruleList is the accumulator half of that interface, which every provider shares.
type ruleList struct{ rules []fwRule }

func (l *ruleList) add(r fwRule) { l.rules = append(l.rules, r) }

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

// firewallRef is the expression this provider's firewall uses to name an asset,
// for the two clouds whose firewall points at the resource rather than the other
// way round.
//
// Empty means nothing can attach to this type — an Azure SQL database has no
// network interface, a DigitalOcean managed database is not a droplet — and the
// caller must emit nothing rather than a reference to a resource that will not
// exist. attachmentGaps reports what that leaves unprotected.
func firewallRef(acc model.Account, assetID string) string {
	a := acc.Asset(assetID)
	if a == nil {
		return ""
	}
	rt, ok := catalog.Type(acc.Provider, a.Code)
	if !ok || rt.FirewallRef == "" {
		return ""
	}
	return expand(rt.FirewallRef, exprCtx{accountID: acc.ID, assetID: a.ID})
}

// ports renders a rule's destination ports. anyPort is what this provider calls
// "every port"; the three that need it spell it differently and otherwise agree.
func ports(r fwRule, anyPort string) string {
	if r.AnyPort {
		return anyPort
	}
	if r.Lo == r.Hi {
		return fmt.Sprint(r.Lo)
	}
	return fmt.Sprintf("%d-%d", r.Lo, r.Hi)
}

// lowerProto is the protocol as three of the four providers spell it: lowercase,
// with each one's own word for "every protocol". Azure titlecases instead.
func lowerProto(proto, all string) string {
	if proto == "ALL" {
		return all
	}
	return strings.ToLower(proto)
}

// ---- turning outcomes into rules -------------------------------------------

// producesRule reports whether a classified outcome becomes a firewall rule at
// all. The complement — the strategies that produce a comment instead — is what
// notes() explains, so both read this rather than each listing half the table.
func producesRule(s Strategy) bool {
	switch s {
	case StratRefused, StratIAM, StratPublicByDesign, StratEdgeForeign:
		return false
	}
	return true
}

// fwRulesFor converts one classified outcome into the firewall rules it implies:
// an ingress rule on the destination, an egress rule on the source, or both.
//
// Only firewalled endpoints get rules. Everything else was already classified as
// IAM, public-by-design or refused, and produces nothing here.
func fwRulesFor(f Flow, o Outcome) []fwRule {
	if !producesRule(o.Strategy) {
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
	// The classifier tags a source with what it is. Strip all three here, so no
	// renderer has to remember which prefixes reach it — two of the four stripped
	// "cidr:" and two did not, which left the invariant undefined.
	peer := o.Source
	for _, prefix := range []string{"aws_ec2_managed_prefix_list:", "service_tag:", "cidr:"} {
		peer = strings.TrimPrefix(peer, prefix)
	}

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
