// The things generation has to tell the user, in the two places it can: comments
// in the configuration itself, and the warning list shown above it.
//
// A refusal the user never sees is as bad as a silently broken rule, so nothing
// here is optional polish.
package tofu

import (
	"fmt"
	"strconv"
	"strings"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// Severity separates "the configuration will not do what the chart says" from
// "it will, and here is something worth knowing".
//
// Without the split every warning looks equally urgent, which trains the user to
// read none of them — and a load balancer with no backends then ships behind a
// line about a project ID.
const (
	SevError  = "error"
	SevAdvice = "advice"
)

// Warning is one thing the user has to know, and what it is about.
//
// The IDs are the point. They are what lets the generate panel select the
// offending line and the canvas mark the offending node; a warning that cannot be
// traced back to something on the chart is one the user has to find by reading
// names, which is how a load balancer shipped with no backends.
type Warning struct {
	Severity  string `json:"severity"`
	Text      string `json:"text"`
	AccountID string `json:"accountId,omitempty"`
	AssetID   string `json:"assetId,omitempty"`
	ConnID    string `json:"connId,omitempty"`
}

// errorAbout and adviceAbout are the two constructors, so a Severity is never
// spelled at a call site and a reporter cannot quietly invent a third level.
func errorAbout(w Warning, format string, args ...any) Warning {
	w.Severity, w.Text = SevError, fmt.Sprintf(format, args...)
	return w
}

func adviceAbout(w Warning, format string, args ...any) Warning {
	w.Severity, w.Text = SevAdvice, fmt.Sprintf(format, args...)
	return w
}

// about names the asset a warning concerns, and the account holding it.
func about(acc model.Account, a model.Asset) Warning {
	return Warning{AccountID: acc.ID, AssetID: a.ID}
}

// Errors reports whether anything here says the configuration will not do what
// the chart says. The canvas badge and the panel's ordering both ask this.
func Errors(ws []Warning) bool {
	for _, w := range ws {
		if w.Severity == SevError {
			return true
		}
	}
	return false
}

// attachmentGaps lists assets whose firewall could not be wired to them here.
// Azure attaches a security group to a network interface or a subnet, and a chart
// does not describe either, so those associations are left to the user.
func attachmentGaps(w *writer, s model.Session, guarded map[string]bool) {
	var lines []string
	for _, acc := range s.Accounts {
		for _, a := range acc.Assets {
			if !guarded[a.ID] {
				continue
			}
			rt, ok := catalog.Type(acc.Provider, a.Code)
			if !ok {
				continue
			}
			if attached(a, rt) {
				continue
			}
			lines = append(lines, fmt.Sprintf("#   %s (%s) — attach its firewall yourself", a.Name, rt.TofuType))
		}
	}
	if len(lines) == 0 {
		return
	}
	w.blank()
	w.line("# Rules were generated for these, but nothing here attaches them:")
	for _, l := range lines {
		w.line("%s", l)
	}
}

// blankChoiceWarnings reports a field the chart opted into and then left empty.
//
// Choosing "Custom AMI ID" and typing nothing is the case this exists for: it
// reaches generation as ami = "", a resource that looks complete and cannot
// launch. Blank is accepted while editing so the drawer still renders, then
// refused here — the same treatment a blank port gets.
//
// The rule is deliberately about the gate rather than about any particular field.
// An ungated field left blank is absence, and has a default behind it; a gated one
// is a question the user chose to be asked and did not answer.
func blankChoiceWarnings(s model.Session) []Warning {
	var out []Warning
	for _, acc := range s.Accounts {
		for _, a := range acc.Assets {
			rt, ok := catalog.Type(acc.Provider, a.Code)
			if !ok {
				continue
			}
			for _, f := range rt.Arguments() {
				if f.RequiresValue == "" || !required(f.RequiresParam, f.RequiresValue, a) {
					continue
				}
				v, isText := a.Params[f.ParamKey()].(string)
				if !isText || strings.TrimSpace(v) != "" {
					continue
				}
				out = append(out, errorAbout(about(acc, a),
					"%s chose %q but left %s empty — enter a value, or pick a different option",
					a.Name, f.RequiresValue, f.Label))
			}
		}
	}
	return out
}

// unreachableWarnings reports an asset that is allowed inbound from outside its
// own network but has no address anything out there could reach.
//
// A firewall rule permits traffic; it does not deliver it. An instance with no
// public address and a rule allowing port 22 generates cleanly, applies cleanly,
// and cannot be connected to — the failure looks like a firewall problem and is
// not one.
//
// Only traffic from outside the account needs a public address. A same-account
// peer resolves to a security group reference over private addressing, so
// warning about it would be a false alarm on the most common case of all.
func unreachableWarnings(r Report) []Warning {
	var out []Warning
	for _, f := range r.Flows {
		if f.To.Asset == nil || f.To.Account == nil || f.To.Type.Network != catalog.NetFirewalled {
			continue
		}
		// The internet and a hardcoded address are outside by definition; an asset
		// is outside when it sits in another account, because the two VPCs have no
		// private path between them.
		external := f.From.Ref.Type != model.NodeAsset ||
			f.From.Account == nil || f.From.Account.ID != f.To.Account.ID
		if !external {
			continue
		}
		// A refused outcome emits nothing, so it grants no access to warn about.
		grants := false
		for _, o := range f.Outcomes {
			if o.Strategy != StratRefused {
				grants = true
				break
			}
		}
		if !grants {
			continue
		}
		if reason := noPublicAddress(f.To.Type, f.To.Asset); reason != "" {
			w := about(*f.To.Account, *f.To.Asset)
			w.ConnID = f.ConnID
			out = append(out, errorAbout(w, "%s is allowed inbound from %s, but %s",
				f.To.Asset.Name, f.From.Label, reason))
		}
	}
	return out
}

// originWarnings reports what a CDN fetches from, where the rule path cannot say
// it.
//
// Three things are invisible to the classifier. An edge with nothing drawn behind
// it produces no flow at all, so nothing else would mention that it serves a
// placeholder. A line drawn with no rule on it likewise produces no flow, and on
// GCP that means the health check is blocked and every backend stays unhealthy —
// a load balancer that looks complete and returns 502. And a listening port of
// 443 in front of a plaintext proxy reads as encrypted from the chart and is not.
func originWarnings(s model.Session, origins map[string]wiring) []Warning {
	var out []Warning
	// Which connection each edge's problem belongs to, so the panel can select the
	// line rather than only naming it.
	conn := map[string]string{}
	connected := map[string]bool{}
	drawn := map[string][]model.Rule{}
	for _, p := range originPairs(s) {
		connected[p.edge.Asset.ID] = true
		conn[p.edge.Asset.ID] = p.connID
		drawn[p.edge.Asset.ID] = p.rules
	}
	for _, acc := range s.Accounts {
		for _, a := range acc.Assets {
			rt, ok := catalog.Type(acc.Provider, a.Code)
			if !ok || rt.Network != catalog.NetEdge {
				continue
			}
			w := about(acc, a)
			w.ConnID = conn[a.ID]
			switch {
			case origins[a.ID].reason != "":
				out = append(out, errorAbout(w, "%s: %s", a.Name, origins[a.ID].reason))
			case !connected[a.ID]:
				out = append(out, errorAbout(w,
					"%s has nothing drawn behind it, so it fetches from a placeholder — connect it to the asset it fronts",
					a.Name))
			}
			// A detached distribution fetches from the placeholder, so the chart
			// says one thing and the configuration does another — which is the
			// definition of an error here, even though the state is deliberate.
			// Left on by accident it is a CDN serving origin.example.com.
			if detached(Endpoint{Account: &acc, Asset: &a, Type: rt}) {
				out = append(out, errorAbout(w,
					"%s is detached from its VPC origin, so it fetches from a placeholder — apply this, make the change, then turn %q off",
					a.Name, paramLabel(rt, catalog.ParamDetachVPCOrigin)))
			}
			// The ports the distribution fetches on and the ports the line opens are
			// two different facts, and this is the only thing that keeps them in
			// step. Under match-viewer CloudFront uses both, so an origin reachable
			// on 443 and nothing else fails every plaintext request while the chart
			// and the generated file both look correct.
			if origins[a.ID].reason == "" && connected[a.ID] {
				for _, port := range originPortsUnpermitted(
					Endpoint{Account: &acc, Asset: &a, Type: rt}, drawn[a.ID]) {
					out = append(out, errorAbout(w,
						"%s fetches its origin on %s, and the line permits no rule for that port — add one, or change the origin port",
						a.Name, port))
				}
			}
			// The proxy this emits speaks HTTP, so 443 would serve plaintext on the
			// port everything assumes is TLS.
			if port, _ := a.Params[catalog.ParamFrontendPort].(string); port == "443" {
				out = append(out, errorAbout(about(acc, a),
					"%s listens on 443 but terminates no TLS — traffic on it is plaintext", a.Name))
			}
		}
	}
	return out
}

// notes records the connections that produced no rule, in the file itself. Someone
// reading the configuration later has no access to the UI warnings.
func notes(w *writer, r Report) {
	var lines []string
	for _, f := range r.Flows {
		for _, o := range f.Outcomes {
			if producesRule(o.Strategy) {
				continue
			}
			lines = append(lines, fmt.Sprintf("#   %s → %s (%s/%s): %s",
				f.From.Label, f.To.Label, o.Rule.Protocol, portOrAny(o.Rule.Port), o.Reason))
		}
	}
	if len(lines) == 0 {
		return
	}
	w.blank()
	w.line("# Declared connections that produced no firewall rule:")
	for _, l := range lines {
		w.line("%s", l)
	}
}
func portOrAny(p string) string {
	if p == "" {
		return "(unset)"
	}
	return p
}

// externalIPs records the hardcoded addresses a chart uses. Nothing is created for
// them — they only supply a CIDR to the rules above.
func externalIPs(w *writer, s model.Session) {
	if len(s.ExternalIPs) == 0 {
		return
	}
	w.blank()
	w.line("# Hardcoded addresses. Not managed here — their CIDRs appear in the rules above.")
	for _, e := range s.ExternalIPs {
		w.line("#   %s — %s", e.Label, e.IP)
	}
}

// accountSettingWarnings names an account setting the chart left blank where the
// generated configuration then falls back to a placeholder.
//
// The GCP project is the case it exists for: var.<account>_project defaults to
// "my-project", and a plan against someone else's project fails with a
// permissions error that names anything but the setting that was never filled in.
func accountSettingWarnings(s model.Session) []Warning {
	var out []Warning
	for _, acc := range s.Accounts {
		p, ok := catalog.Get(acc.Provider)
		if !ok {
			continue
		}
		for _, v := range p.Variables {
			if v.DefaultFromParam == "" || p.AccountParam(acc.Params, v.DefaultFromParam) != "" {
				continue
			}
			label := v.DefaultFromParam
			for _, f := range p.AccountParams {
				if f.Key == v.DefaultFromParam {
					label = f.Label
				}
			}
			out = append(out, adviceAbout(Warning{AccountID: acc.ID},
				"%s has no %q set, so the configuration falls back to %s — set it on the account, or pass -var %s=…",
				acc.Name, label, v.Default, expand(v.Name, exprCtx{accountID: acc.ID})))
		}
	}
	return out
}

// originPortsUnpermitted lists the ports this edge will connect on that no rule
// in the origin-fetch direction allows.
//
// Only AWS separates the two: GCP's backend port is still the one drawn on the
// line, so an edge whose adapter names no ports has nothing to disagree about.
func originPortsUnpermitted(edge Endpoint, rules []model.Rule) []string {
	if edge.Account == nil || edge.Account.Provider != "aws" {
		return nil
	}
	var out []string
	for _, port := range originPortsUsed(edge) {
		n, err := strconv.Atoi(port)
		if err != nil {
			// A blank or unparseable port is caught where it is set, not here.
			continue
		}
		if !permits(rules, n) {
			out = append(out, port)
		}
	}
	return out
}

func permits(rules []model.Rule, port int) bool {
	for _, r := range rules {
		lo, hi, any := r.Ports()
		if any || (port >= lo && port <= hi) {
			return true
		}
	}
	return false
}
