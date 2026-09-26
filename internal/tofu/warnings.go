// The things generation has to tell the user, in the two places it can: comments
// in the configuration itself, and the warning list shown above it.
//
// A refusal the user never sees is as bad as a silently broken rule, so nothing
// here is optional polish.
package tofu

import (
	"fmt"
	"strings"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

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
func blankChoiceWarnings(s model.Session) []string {
	var out []string
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
				out = append(out, fmt.Sprintf(
					"%s → %s chose %q but left %s empty — enter a value, or pick a different option",
					acc.Name, a.Name, f.RequiresValue, f.Label))
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
func unreachableWarnings(r Report) []string {
	var out []string
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
			out = append(out, fmt.Sprintf("%s → %s is allowed inbound from %s, but %s",
				f.To.Account.Name, f.To.Asset.Name, f.From.Label, reason))
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
func accountSettingWarnings(s model.Session) []string {
	var out []string
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
			out = append(out, fmt.Sprintf("%s has no %q set, so the configuration falls back to %s — set it on the account, or pass -var %s=…",
				acc.Name, label, v.Default, expand(v.Name, exprCtx{accountID: acc.ID})))
		}
	}
	return out
}
