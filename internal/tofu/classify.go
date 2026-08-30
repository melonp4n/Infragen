// Package tofu turns a chart into OpenTofu configuration.
//
// Classification is separated from emission because it is where the judgement
// lives: whether a connection can become a firewall rule at all, and what to tell
// the user when it cannot. See documentation/TOFU-MAPPING.md for the reasoning.
package tofu

import (
	"fmt"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// Strategy is what one allowed-traffic entry turns into.
type Strategy string

const (
	// StratSecurityGroupRef references the peer's security group instead of an
	// address. Same account and provider only, but it cannot rot.
	StratSecurityGroupRef Strategy = "security-group-ref"
	// StratCIDR uses an address: a literal, or a reference to the peer resource's
	// address attribute — which is also the dependency edge.
	StratCIDR Strategy = "cidr"
	// StratEdgeNative uses the provider's own CDN construct: a managed prefix list
	// on AWS, a service tag on Azure, the load balancer ranges on GCP.
	StratEdgeNative Strategy = "edge-native"
	// StratEdgeForeign is a CDN reaching an origin in another cloud. No IP-based
	// control exists; the origin must authenticate the edge instead.
	StratEdgeForeign Strategy = "edge-foreign"
	// StratIAM is traffic to or from a service endpoint. Access is governed by
	// policy, not by a firewall, and the port is irrelevant.
	StratIAM Strategy = "iam"
	// StratPublicByDesign is reaching something that is already public, such as a
	// CDN. Nothing to generate; the chart is recording intent.
	StratPublicByDesign Strategy = "public-by-design"
	// StratRefused means no correct rule can be produced. Reason says why and what
	// the user can do about it.
	StratRefused Strategy = "refused"
)

// Pseudo network kinds for the two node types that are not catalog resources.
const (
	kindInternet = "internet"
	kindExtIP    = "extip"
)

// Endpoint is one end of a connection with its catalog entry resolved.
type Endpoint struct {
	Ref     model.NodeRef
	Label   string
	Account *model.Account
	Asset   *model.Asset
	Type    catalog.ResourceType
	ExtIP   *model.ExternalIP
}

// Network reports how this endpoint is reached, using the catalog's kinds plus
// the two pseudo kinds for the internet and hardcoded addresses.
func (e Endpoint) Network() string {
	switch e.Ref.Type {
	case model.NodeInternet:
		return kindInternet
	case model.NodeExtIP:
		return kindExtIP
	default:
		return e.Type.Network
	}
}

// Outcome is one allowed-traffic entry and what it becomes.
type Outcome struct {
	Rule     model.Rule
	Strategy Strategy
	// Source is the address, expression or construct the rule allows. Its meaning
	// depends on Strategy, and it is empty when nothing is emitted.
	Source string
	// Reason explains a refusal, or names what the user must configure. Empty when
	// the outcome needs no explanation.
	Reason string
}

// Flow is one direction of one connection: what From may initiate towards To.
type Flow struct {
	ConnID   string
	From, To Endpoint
	Outcomes []Outcome
}

// Report is the full classification of a chart.
type Report struct {
	Flows []Flow
}

// Classify resolves every connection into directional flows and decides what each
// rule becomes. Directions with no rules are dropped — an empty rule list means
// blocked, and there is nothing to say about it.
//
// Run model.Validate and model.Normalise first; this assumes both.
func Classify(s model.Session) Report {
	var r Report
	for _, c := range s.Connections {
		a, b := resolve(s, c.A), resolve(s, c.B)
		for _, d := range []struct {
			from, to Endpoint
			rules    []model.Rule
		}{{a, b, c.AToB}, {b, a, c.BToA}} {
			if len(d.rules) == 0 {
				continue
			}
			f := Flow{ConnID: c.ID, From: d.from, To: d.to}
			for _, rule := range d.rules {
				f.Outcomes = append(f.Outcomes, classifyRule(d.from, d.to, rule))
			}
			r.Flows = append(r.Flows, f)
		}
	}
	return r
}

func resolve(s model.Session, ref model.NodeRef) Endpoint {
	e := Endpoint{Ref: ref, Label: s.Label(ref)}
	switch ref.Type {
	case model.NodeExtIP:
		e.ExtIP = s.ExternalIP(ref.ID)
	case model.NodeAsset:
		e.Account = s.Account(ref.AccountID)
		if e.Account != nil {
			e.Asset = e.Account.Asset(ref.AssetID)
			if e.Asset != nil {
				e.Type, _ = catalog.Type(e.Account.Provider, e.Asset.Code)
			}
		}
	}
	return e
}

// classifyRule decides what one rule becomes, given the direction it runs in.
//
// The order of these cases matters. An explicit CIDR override wins over
// everything, because it is the user telling us the address directly. After that,
// service endpoints and edges are checked before the firewalled-to-firewalled
// case, since those are the ones that must never become firewall rules.
func classifyRule(from, to Endpoint, rule model.Rule) Outcome {
	out := Outcome{Rule: rule}

	// Validate rejects dangling references, so this should be unreachable — but a
	// classifier that panics on bad input is worse than one that reports it.
	for _, e := range []Endpoint{from, to} {
		if e.Ref.Type == model.NodeAsset && e.Asset == nil {
			out.Strategy = StratRefused
			out.Reason = "an endpoint of this connection no longer exists"
			return out
		}
	}

	// A rule with no port yet generates nothing. This is the fail-closed half of
	// the port handling: blank must never quietly become "all ports", because a
	// rule abandoned half-written would then open everything.
	if rule.PortUnset() {
		out.Strategy = StratRefused
		out.Reason = `no port set — enter a port, a range like 8000-8080, or "*" for all ports`
		return out
	}

	if cidr, ok := rule.OverrideCIDR(); ok {
		out.Strategy, out.Source = StratCIDR, cidr
		return out
	}

	fromNet, toNet := from.Network(), to.Network()

	// Anything involving a service endpoint is governed by IAM, not a firewall.
	// This is checked early because it is the mistake with the worst consequence:
	// a security-group rule here applies cleanly and controls nothing.
	if fromNet == catalog.NetServiceEndpoint || toNet == catalog.NetServiceEndpoint {
		out.Strategy = StratIAM
		out.Reason = "access is governed by IAM or a resource policy, not a firewall — the port is irrelevant"
		return out
	}

	// A CDN is public already, so reaching one needs no rule.
	if toNet == catalog.NetEdge {
		if fromNet == kindInternet || fromNet == kindExtIP {
			out.Strategy = StratPublicByDesign
			out.Reason = "a CDN is publicly reachable by construction"
			return out
		}
		out.Strategy = StratRefused
		out.Reason = fmt.Sprintf("%s exposes only a hostname, and an egress rule needs an address", to.Label)
		return out
	}

	// A CDN fetching from an origin. Only the provider's own construct can express
	// this, so it works within a cloud and not across them.
	if fromNet == catalog.NetEdge {
		if toNet != catalog.NetFirewalled {
			out.Strategy = StratRefused
			out.Reason = fmt.Sprintf("%s cannot be an origin for a CDN", to.Label)
			return out
		}
		if from.Account != nil && to.Account != nil && from.Account.Provider == to.Account.Provider {
			out.Strategy = StratEdgeNative
			out.Source = edgeConstruct(from.Account.Provider)
			return out
		}
		out.Strategy = StratEdgeForeign
		out.Reason = "a CDN has no stable egress IP, and no cross-cloud address construct exists — " +
			"authenticate the edge at the origin (a shared secret or X-Azure-FDID header over HTTPS) instead"
		return out
	}

	// From here the destination must be something a rule can attach to.
	if toNet != catalog.NetFirewalled && toNet != kindInternet && toNet != kindExtIP {
		out.Strategy = StratRefused
		out.Reason = fmt.Sprintf("%s has nothing to attach a rule to", to.Label)
		return out
	}

	// Egress towards the public internet or a hardcoded address.
	if toNet == kindInternet || toNet == kindExtIP {
		if fromNet != catalog.NetFirewalled {
			out.Strategy = StratRefused
			out.Reason = fmt.Sprintf("%s has no firewall to place an egress rule on", from.Label)
			return out
		}
		out.Strategy, out.Source = StratCIDR, peerLiteral(to)
		return out
	}

	// Ingress from the public internet or a hardcoded address.
	if fromNet == kindInternet || fromNet == kindExtIP {
		out.Strategy, out.Source = StratCIDR, peerLiteral(from)
		return out
	}

	// Both ends are firewalled. Within one account and provider a group reference
	// beats an address: there is no IP to go stale.
	if from.Account != nil && to.Account != nil && from.Account.ID == to.Account.ID {
		out.Strategy, out.Source = StratSecurityGroupRef, from.Asset.ID
		return out
	}

	// Cross account or cross cloud: the rule needs the source's address.
	return sourceAddress(from)
}

// sourceAddress resolves a cross-account peer to something usable as a rule
// source, or refuses with the reason and the fix.
func sourceAddress(from Endpoint) Outcome {
	out := Outcome{}
	switch from.Type.AddressKind {
	case catalog.AddrStaticIP:
		out.Strategy = StratCIDR
		out.Source = fmt.Sprintf("${%s.%s.%s}/32", from.Type.TofuType, resourceName(from.Asset.ID), from.Type.AddressAttr)
	case catalog.AddrEphemeralIP:
		if staticEnabled(from.Asset) && from.Type.StaticAddr != nil {
			out.Strategy = StratCIDR
			out.Source = fmt.Sprintf("${%s.%s.%s}/32", from.Type.StaticAddr.TofuType, resourceName(from.Asset.ID), from.Type.StaticAddr.Attr)
			return out
		}
		out.Strategy = StratRefused
		out.Reason = fmt.Sprintf("%s.%s changes when the instance restarts, so the rule would break "+
			"silently — enable \"Static public IP\" on %s", from.Type.TofuType, from.Type.AddressAttr, from.Label)
	case catalog.AddrHostname:
		out.Strategy = StratRefused
		out.Reason = fmt.Sprintf("%s exposes only a hostname (%s.%s) and firewall rules need an address — "+
			"put an explicit CIDR in the rule's note to override", from.Label, from.Type.TofuType, from.Type.AddressAttr)
	default:
		out.Strategy = StratRefused
		out.Reason = fmt.Sprintf("%s exposes no address to use as a rule source — "+
			"put an explicit CIDR in the rule's note to override", from.Label)
	}
	return out
}

// peerLiteral is the literal address for a node that is not a managed resource.
func peerLiteral(e Endpoint) string {
	if e.Ref.Type == model.NodeExtIP && e.ExtIP != nil {
		return e.ExtIP.IP
	}
	return "0.0.0.0/0"
}

// edgeConstruct names the provider's own way of allowing its CDN to reach an
// origin. These are the only correct answers; a CIDR is not one.
func edgeConstruct(provider string) string {
	switch provider {
	case "aws":
		return "aws_ec2_managed_prefix_list:com.amazonaws.global.cloudfront.origin-facing"
	case "azure":
		return "service_tag:AzureFrontDoor.Backend"
	case "gcp":
		return "cidr:130.211.0.0/22,35.191.0.0/16"
	case "digitalocean":
		return "cidr:0.0.0.0/0"
	}
	return ""
}

func staticEnabled(a *model.Asset) bool {
	if a == nil {
		return false
	}
	on, _ := a.Params[catalog.ParamStaticPublicIP].(bool)
	return on
}

// resourceName derives an HCL resource name from an asset ID. Addresses must not
// depend on display names: a name-derived address means renaming an asset
// destroys and recreates the resource on the next apply.
func resourceName(assetID string) string {
	return assetID
}

// NeedsAction reports whether an outcome requires the user to do something. An
// IAM-governed edge or a public CDN is worth explaining inline, but it is not a
// problem — listing those as warnings alongside real refusals trains the user to
// ignore the list.
func (o Outcome) NeedsAction() bool {
	return o.Strategy == StratRefused || o.Strategy == StratEdgeForeign
}

// Warnings returns only the outcomes the user must act on: rules that could not be
// generated, and CDN edges that no firewall can express. A refusal the user never
// sees is as bad as a silently broken rule.
func (r Report) Warnings() []string {
	var out []string
	for _, f := range r.Flows {
		for _, o := range f.Outcomes {
			if !o.NeedsAction() {
				continue
			}
			label := fmt.Sprintf("%s → %s", f.From.Label, f.To.Label)
			port := o.Rule.Port
			if port == "" {
				port = "(unset)"
			}
			label += fmt.Sprintf(" (%s/%s)", o.Rule.Protocol, port)
			prefix := "no rule generated"
			if o.Strategy == StratEdgeForeign {
				prefix = "not expressible as a firewall rule"
			}
			out = append(out, fmt.Sprintf("%s — %s: %s", prefix, label, o.Reason))
		}
	}
	return out
}

// FlowFor returns the classified flow for one direction of a connection, named by
// the endpoint that initiates it. The UI uses this to explain what a rule will
// become while it is being written, rather than only after generating.
func (r Report) FlowFor(connID string, from model.NodeRef) (Flow, bool) {
	for _, f := range r.Flows {
		if f.ConnID == connID && f.From.Ref.Key() == from.Key() {
			return f, true
		}
	}
	return Flow{}, false
}
