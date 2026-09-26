package tofu

import (
	"fmt"
	"strings"

	"infrachart/internal/model"
)

// GCP firewall rules live on the network and select instances by tag, so there is
// no per-asset group. Each asset gets a generated tag.
type gcpFirewall struct{ ruleList }

func (f *gcpFirewall) render(w *writer, acc model.Account) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		for i, r := range grouped[asset] {
			w.blank()
			w.line("# %s", r.Comment)
			w.block(fmt.Sprintf("resource %q %q", "google_compute_firewall", ruleName(r, i)), func() {
				w.arg("provider", "google."+acc.ID)
				w.arg("name", quote(dashed(ruleName(r, i))))
				w.arg("network", fmt.Sprintf("google_compute_network.%s.name", acc.ID))
				w.arg("direction", quote(strings.ToUpper(r.Dir)))
				w.block("allow", func() {
					w.arg("protocol", quote(lowerProto(r.Proto, "all")))
					if !r.AnyPort && r.Proto != "ALL" {
						w.arg("ports", fmt.Sprintf("[%s]", quote(ports(r, ""))))
					}
				})
				// Tags select which instances the rule applies to. Ranges are the
				// only peer representation GCP offers here.
				w.arg("target_tags", fmt.Sprintf("[%s]", quote(dashed(r.Asset))))
				key := "source_ranges"
				if r.Dir == dirEgress {
					key = "destination_ranges"
				}
				w.arg(key, gcpRanges(r))
			})
		}
	}
}

// gcpRanges renders the peer as a list of CIDRs. A same-account pair uses the
// peer's tag instead, which GCP supports for source but not destination.
func gcpRanges(r fwRule) string {
	if r.Kind == peerSecurityGroup {
		return fmt.Sprintf("[%s]", quote("10.0.0.0/8"))
	}
	parts := strings.Split(r.Peer, ",")
	for i, p := range parts {
		parts[i] = interp(strings.TrimSpace(p))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
