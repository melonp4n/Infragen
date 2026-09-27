// What a CDN fetches from, and what each cloud needs before it can.
//
// A line from an edge to an asset says "this is my origin". Until this existed
// the line produced a firewall rule and nothing else: CloudFront kept fetching
// from a placeholder domain, and Google's health-check ranges were opened on an
// instance with no load balancer to send anything. A rule permits traffic; it
// does not deliver it.
//
// The shape mirrors firewall.go deliberately. One interface, one implementation
// per cloud, chosen by provider key — so a provider's rules are in one file, and
// a fifth cloud is a new file rather than a new branch in an existing one.
package tofu

import (
	"fmt"
	"strconv"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// origin turns an edge-to-origin connection into the resources and arguments the
// edge needs. wire is given the connection's rules in the edge-to-origin
// direction: the port the user drew is the port the origin is fetched on, and a
// port invented here would contradict the chart — opening one port on the asset
// and talking to another.
// Origin.RequiresParam is not checked here. What an origin must have turned on
// first depends on how the edge reaches it — a VPC origin needs no address at
// all — so each adapter raises it on the paths where it is true.
type origin interface {
	wire(edge, from Endpoint, rules []model.Rule) wiring
	// unwired is the same edge with nothing drawn behind it. CloudFront cannot
	// declare no origin at all, so the case has an answer rather than being
	// skipped.
	unwired(edge Endpoint) wiring
}

// wiring is what an edge asset gains because something is drawn behind it.
//
// Everything here attaches to the edge. Origin-side resources are catalog
// companions gated on a directive instead: an elastic address and a function URL
// belong to the asset that owns them, whether or not a CDN happens to point at
// it, and emitting them from here would make them appear and disappear as lines
// are drawn.
type wiring struct {
	fixed      []catalog.Fixed
	companions []catalog.Companion
	vars       []catalog.Variable
	// reason non-empty means no correct wiring exists. It says what to do
	// instead, because a refusal with no fix is a dead end.
	reason string
}

func newOrigin(provider string) origin {
	switch provider {
	case "aws":
		return awsOrigin{}
	case "gcp":
		return gcpOrigin{}
	}
	return nil
}

// originWiring resolves what each edge asset gains from the lines drawn behind
// it, keyed by asset ID.
//
// It walks connections rather than the classified report on purpose. A line with
// no rule on it still declares an origin, and Classify only produces a flow where
// rules exist — so reading the report would make a distribution's origin vanish
// the moment someone cleared the port field to retype it.
func originWiring(s model.Session) map[string]wiring {
	out := map[string]wiring{}
	// Every edge first, so one with no line behind it still gets an answer.
	for _, acc := range s.Accounts {
		adapter := newOrigin(acc.Provider)
		if adapter == nil {
			continue
		}
		for _, a := range acc.Assets {
			rt, ok := catalog.Type(acc.Provider, a.Code)
			if !ok || rt.Network != catalog.NetEdge {
				continue
			}
			out[a.ID] = adapter.unwired(Endpoint{Account: &acc, Asset: &a, Type: rt})
		}
	}
	wired := map[string]bool{}
	for _, p := range originPairs(s) {
		id := p.edge.Asset.ID
		w := resolveOrigin(p.edge, p.from, p.rules, p.reverse)
		if wired[id] {
			// One origin per edge. A second needs cache behaviours or a second
			// backend to route between them, which the chart does not describe.
			w = wiring{reason: fmt.Sprintf(
				"%s already fetches from another origin, and only one is generated — remove one of the lines",
				p.edge.Asset.Name)}
		}
		// A refusal still has to leave valid configuration behind: a distribution
		// with no origin block at all will not plan, so the reason travels with the
		// unwired form rather than instead of it.
		if w.reason != "" {
			w = wiring{reason: w.reason, fixed: out[id].fixed, companions: out[id].companions, vars: out[id].vars}
		} else {
			wired[id] = true
		}
		out[id] = w
	}
	return out
}

// originPair is one edge-to-origin line, oriented so that edge is the CDN.
//
// Both rule lists are carried. Which of a connection's two directions is "A to B"
// depends on which connector the user clicked first, so a rule in the other one is
// an ordinary mistake — and telling someone "there is no rule on this line" when
// they have just written one is worse than saying nothing.
type originPair struct {
	connID  string
	edge    Endpoint
	from    Endpoint
	rules   []model.Rule // edge → origin, the direction an origin fetch runs
	reverse []model.Rule // the other list, so a rule in it can be named
}

// originPairs finds the connections that declare an origin. A connection between
// two edges, or between an edge and the internet, declares nothing.
func originPairs(s model.Session) []originPair {
	var out []originPair
	for _, c := range s.Connections {
		a, b := resolve(s, c.A), resolve(s, c.B)
		switch {
		case a.Network() == catalog.NetEdge && b.Ref.Type == model.NodeAsset && b.Network() != catalog.NetEdge:
			out = append(out, originPair{connID: c.ID, edge: a, from: b, rules: c.AToB, reverse: c.BToA})
		case b.Network() == catalog.NetEdge && a.Ref.Type == model.NodeAsset && a.Network() != catalog.NetEdge:
			out = append(out, originPair{connID: c.ID, edge: b, from: a, rules: c.BToA, reverse: c.AToB})
		}
	}
	return out
}

// resolveOrigin is the shared half of the decision: everything true of every
// cloud, settled once so no provider restates it. What survives goes to the
// provider's own adapter.
func resolveOrigin(edge, from Endpoint, rules, reverse []model.Rule) wiring {
	if edge.Asset == nil || from.Asset == nil || edge.Account == nil || from.Account == nil {
		return wiring{reason: "an endpoint of this connection no longer exists"}
	}
	// Cross-cloud is refused earlier and for a different reason — no address
	// construct spans two clouds — so the adapter is never asked.
	if edge.Account.Provider != from.Account.Provider {
		return wiring{reason: fmt.Sprintf("%s and %s are in different clouds, and no edge can fetch across them",
			edge.Asset.Name, from.Asset.Name)}
	}
	if from.Type.Origin == nil {
		return wiring{reason: fmt.Sprintf("%s cannot be a CDN origin — nothing there answers an HTTP request",
			from.Asset.Name)}
	}
	// The direction a rule was written in decides nothing about which end is which
	// — that is settled by whichever connector was clicked first. So a rule in the
	// other list is a misplaced rule, and saying so is the whole job here: the
	// previous message claimed the line had no rule on it, which the user could see
	// was untrue and therefore taught them to distrust the rest.
	if len(rules) == 0 {
		if len(reverse) > 0 {
			return wiring{reason: fmt.Sprintf(
				"the rule is on %q, and an origin fetch runs the other way — move it to %q, or no backend is generated",
				from.Label+" → "+edge.Label, edge.Label+" → "+from.Label)}
		}
		return wiring{reason: fmt.Sprintf(
			"no rule on this line, so nothing says which port %s serves — add one to %q",
			from.Asset.Name, edge.Label+" → "+from.Label)}
	}
	adapter := newOrigin(edge.Account.Provider)
	if adapter == nil {
		return wiring{reason: fmt.Sprintf("%s has no origin wiring in this tool yet",
			edge.Account.Provider)}
	}
	return adapter.wire(edge, from, rules)
}

// originPort is the one port an origin is fetched on.
//
// An origin has exactly one, so a wildcard or a range cannot name it, and a blank
// port is unspecified rather than a default — the same fail-closed reading a
// firewall rule gives it. Guessing 80 here would quietly contradict a chart that
// says 8080.
func originPort(rules []model.Rule) (string, string) {
	for _, r := range rules {
		if r.PortUnset() {
			continue
		}
		lo, hi, any := r.Ports()
		switch {
		case any:
			return "", `an origin is fetched on one port, so "*" cannot name it — set the port the origin serves`
		case lo != hi:
			return "", fmt.Sprintf("an origin is fetched on one port, so the range %s cannot name it — set the port the origin serves", r.Port)
		}
		return strconv.Itoa(lo), ""
	}
	return "", "the rule on this line has no port — set the port the origin serves"
}

// OriginNote is what is wrong with this connection's origin wiring in the named
// direction, or empty.
//
// The drawer needs this directly rather than through Classify. The refusal it
// most needs to show happens when the direction has no rules at all, and Classify
// produces outcomes only where rules exist — so the one case worth shouting about
// is the one case nothing would have rendered.
func OriginNote(s model.Session, connID, dir string) string {
	for _, p := range originPairs(s) {
		if p.connID != connID {
			continue
		}
		// The note belongs on the direction an origin fetch runs, which is the
		// direction the user has to write in.
		if (dir == "aToB") != sameRef(p.edge.Ref, connRefA(s, connID)) {
			continue
		}
		return resolveOrigin(p.edge, p.from, p.rules, p.reverse).reason
	}
	return ""
}

func connRefA(s model.Session, connID string) model.NodeRef {
	for _, c := range s.Connections {
		if c.ID == connID {
			return c.A
		}
	}
	return model.NodeRef{}
}

func sameRef(a, b model.NodeRef) bool { return a.Key() == b.Key() }

// originExpr is the hostname an edge fetches from, for the clouds whose edge
// takes a name. The expression is passed in because one type has two of them:
// the public name and the private one.
func originExpr(from Endpoint, expr string) string {
	return expand(expr, exprCtx{
		accountID: from.Account.ID,
		assetID:   from.Asset.ID,
		params:    from.Asset.Params,
	})
}
