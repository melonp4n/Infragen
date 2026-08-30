package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// find returns the outcomes for the flow running from one label towards another.
func find(t *testing.T, r Report, from, to string) []Outcome {
	t.Helper()
	for _, f := range r.Flows {
		if strings.Contains(f.From.Label, from) && strings.Contains(f.To.Label, to) {
			return f.Outcomes
		}
	}
	t.Fatalf("no flow %q → %q", from, to)
	return nil
}

func classifySeed(t *testing.T) (model.Session, Report) {
	t.Helper()
	s := model.Seed()
	if err := model.Validate(&s); err != nil {
		t.Fatalf("seed invalid: %v", err)
	}
	model.Normalise(&s)
	return s, Classify(s)
}

// The seed chart covers most of the strategy table, so it doubles as the
// integration case.
func TestClassifySeed(t *testing.T) {
	_, r := classifySeed(t)

	cases := []struct {
		from, to string
		want     Strategy
	}{
		// Public ingress to a CDN: nothing to generate, a distribution is public.
		{"Internet", "Edge CDN", StratPublicByDesign},
		// CDN to origin within AWS: the managed prefix list.
		{"Edge CDN", "Public ALB", StratEdgeNative},
		// Same account, both firewalled: a group reference, no address involved.
		{"Public ALB", "Web tier EC2", StratSecurityGroupRef},
		{"Web tier EC2", "Orders DB", StratSecurityGroupRef},
		// Egress to the public internet.
		{"Web tier EC2", "Internet", StratCIDR},
		// A service endpoint is never a firewall rule, however plausible the port.
		{"Image resize fn", "Uploads bucket", StratIAM},
		// A hardcoded address supplies its own CIDR.
		{"Office jumphost", "Web tier EC2", StratCIDR},
		{"Office jumphost", "App droplet", StratCIDR},
	}
	for _, c := range cases {
		got := find(t, r, c.from, c.to)
		if len(got) == 0 {
			t.Fatalf("%s → %s: no outcomes", c.from, c.to)
		}
		if got[0].Strategy != c.want {
			t.Errorf("%s → %s: got %s (%s), want %s", c.from, c.to, got[0].Strategy, got[0].Reason, c.want)
		}
	}
}

// The regression that matters most: a service endpoint must never produce
// something a caller could mistake for a firewall rule.
func TestServiceEndpointsNeverProduceRules(t *testing.T) {
	s, r := classifySeed(t)

	isService := func(e Endpoint) bool { return e.Network() == catalog.NetServiceEndpoint }
	found := false
	for _, f := range r.Flows {
		if !isService(f.From) && !isService(f.To) {
			continue
		}
		found = true
		for _, o := range f.Outcomes {
			if o.Strategy != StratIAM {
				t.Errorf("%s → %s: service endpoint produced %s, want iam", f.From.Label, f.To.Label, o.Strategy)
			}
			if o.Source != "" {
				t.Errorf("%s → %s: service endpoint produced a rule source %q", f.From.Label, f.To.Label, o.Source)
			}
		}
	}
	if !found {
		t.Fatal("seed chart has no service-endpoint flow, so this test proved nothing")
	}
	_ = s
}

// The jump host's own CIDR is what the rule allows, not a placeholder.
func TestExternalIPSuppliesItsCIDR(t *testing.T) {
	_, r := classifySeed(t)
	got := find(t, r, "Office jumphost", "Web tier EC2")
	if got[0].Source != "203.0.113.10/32" {
		t.Errorf("source = %q, want the jump host's CIDR", got[0].Source)
	}
}

// Egress to the internet is 0.0.0.0/0, and it must not be confused with ingress —
// the seed has no inbound rule on this connection.
func TestInternetEgressOnly(t *testing.T) {
	_, r := classifySeed(t)
	got := find(t, r, "Web tier EC2", "Internet")
	if got[0].Source != "0.0.0.0/0" {
		t.Errorf("source = %q, want 0.0.0.0/0", got[0].Source)
	}
	for _, f := range r.Flows {
		if f.From.Label == "Internet" && strings.Contains(f.To.Label, "Web tier EC2") {
			t.Error("classified an inbound flow the chart does not declare")
		}
	}
}

// crossCloud builds a two-account chart with one connection, so each cross-account
// case can be exercised in isolation.
func crossCloud(t *testing.T, fromCode, fromProvider, toCode, toProvider string, params map[string]any) Report {
	t.Helper()
	from := model.Account{ID: "acc_from", Name: "From", Provider: fromProvider, Assets: []model.Asset{
		{ID: "asset_from", Code: fromCode, Name: "Source", Params: catalog.Defaults(fromProvider, fromCode)},
	}}
	to := model.Account{ID: "acc_to", Name: "To", Provider: toProvider, Assets: []model.Asset{
		{ID: "asset_to", Code: toCode, Name: "Target", Params: catalog.Defaults(toProvider, toCode)},
	}}
	for k, v := range params {
		from.Assets[0].Params[k] = v
	}
	s := model.Session{
		Version:  model.SchemaVersion,
		Accounts: []model.Account{from, to},
		Connections: []model.Connection{{
			ID:   "conn_x",
			A:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc_from", AssetID: "asset_from"},
			B:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc_to", AssetID: "asset_to"},
			AToB: []model.Rule{{Protocol: "TCP", Port: "443"}},
		}},
	}
	if err := model.Validate(&s); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	model.Normalise(&s)
	return Classify(s)
}

// An ephemeral address is refused, and the refusal names the toggle that fixes it —
// otherwise the user is stuck.
func TestEphemeralAddressRefusedWithAFix(t *testing.T) {
	r := crossCloud(t, "EC2", "aws", "DRP", "digitalocean", nil)
	got := r.Flows[0].Outcomes[0]
	if got.Strategy != StratRefused {
		t.Fatalf("got %s, want refused", got.Strategy)
	}
	if !strings.Contains(got.Reason, "Static public IP") {
		t.Errorf("refusal does not name the fix: %q", got.Reason)
	}
}

// With the toggle on, the rule references the static address resource — which is
// also what gives OpenTofu the dependency edge.
func TestStaticToggleProducesAReference(t *testing.T) {
	r := crossCloud(t, "EC2", "aws", "DRP", "digitalocean",
		map[string]any{catalog.ParamStaticPublicIP: true})
	got := r.Flows[0].Outcomes[0]
	if got.Strategy != StratCIDR {
		t.Fatalf("got %s (%s), want cidr", got.Strategy, got.Reason)
	}
	if got.Source != "${aws_eip.asset_from.public_ip}/32" {
		t.Errorf("source = %q, want a reference to the eip", got.Source)
	}
}

// A droplet's address is stable, so no toggle is needed.
func TestStableAddressReferencedDirectly(t *testing.T) {
	r := crossCloud(t, "DRP", "digitalocean", "EC2", "aws", nil)
	got := r.Flows[0].Outcomes[0]
	if got.Strategy != StratCIDR {
		t.Fatalf("got %s (%s), want cidr", got.Strategy, got.Reason)
	}
	if got.Source != "${digitalocean_droplet.asset_from.ipv4_address}/32" {
		t.Errorf("source = %q", got.Source)
	}
}

// A hostname cannot be a firewall rule source, and the refusal says so.
func TestHostnameSourceRefused(t *testing.T) {
	r := crossCloud(t, "ALB", "aws", "DRP", "digitalocean", nil)
	got := r.Flows[0].Outcomes[0]
	if got.Strategy != StratRefused {
		t.Fatalf("got %s, want refused", got.Strategy)
	}
	if !strings.Contains(got.Reason, "hostname") {
		t.Errorf("reason = %q", got.Reason)
	}
}

// The user's example: a CDN in one cloud reaching an origin in another has no
// IP-based control, and the reason must say what to do instead.
func TestCrossCloudCDNIsRefusedWithGuidance(t *testing.T) {
	r := crossCloud(t, "CDN", "azure", "EC2", "aws", nil)
	got := r.Flows[0].Outcomes[0]
	if got.Strategy != StratEdgeForeign {
		t.Fatalf("got %s, want edge-foreign", got.Strategy)
	}
	if !strings.Contains(got.Reason, "authenticate the edge") {
		t.Errorf("reason does not point at the working control: %q", got.Reason)
	}
}

// The documented escape hatch: a CIDR in the note overrides everything, including
// a refusal that would otherwise apply.
func TestDetailCIDROverridesARefusal(t *testing.T) {
	from := model.Account{ID: "acc_a", Name: "A", Provider: "aws", Assets: []model.Asset{
		{ID: "asset_a", Code: "ALB", Name: "LB", Params: catalog.Defaults("aws", "ALB")},
	}}
	to := model.Account{ID: "acc_b", Name: "B", Provider: "digitalocean", Assets: []model.Asset{
		{ID: "asset_b", Code: "DRP", Name: "Droplet", Params: catalog.Defaults("digitalocean", "DRP")},
	}}
	s := model.Session{
		Version:  model.SchemaVersion,
		Accounts: []model.Account{from, to},
		Connections: []model.Connection{{
			ID:   "conn_x",
			A:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc_a", AssetID: "asset_a"},
			B:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc_b", AssetID: "asset_b"},
			AToB: []model.Rule{{Protocol: "TCP", Port: "443", Detail: "198.51.100.0/24"}},
		}},
	}
	if err := model.Validate(&s); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	model.Normalise(&s)

	got := Classify(s).Flows[0].Outcomes[0]
	if got.Strategy != StratCIDR {
		t.Fatalf("got %s (%s), want cidr", got.Strategy, got.Reason)
	}
	if got.Source != "198.51.100.0/24" {
		t.Errorf("source = %q, want the override", got.Source)
	}
}

// An empty rule list means blocked, so it should produce no flow at all rather
// than an empty one.
func TestBlockedDirectionsAreNotClassified(t *testing.T) {
	_, r := classifySeed(t)
	for _, f := range r.Flows {
		if len(f.Outcomes) == 0 {
			t.Errorf("%s → %s: flow with no outcomes", f.From.Label, f.To.Label)
		}
	}
	// The seed declares eight connections, all one-directional.
	if len(r.Flows) != 8 {
		t.Errorf("got %d flows, want 8 — one per declared direction", len(r.Flows))
	}
}

// Warnings must carry exactly the actionable outcomes. Listing IAM edges and
// public CDNs there too would train the user to ignore the list, and the real
// refusals would go unread.
func TestWarningsAreActionableOnly(t *testing.T) {
	_, seed := classifySeed(t)
	informational := 0
	for _, f := range seed.Flows {
		for _, o := range f.Outcomes {
			if o.Reason != "" && !o.NeedsAction() {
				informational++
			}
		}
	}
	if informational == 0 {
		t.Fatal("seed chart has no informational outcomes, so this test proved nothing")
	}
	if got := len(seed.Warnings()); got != 0 {
		t.Errorf("seed chart needs no action but produced %d warnings: %v", got, seed.Warnings())
	}

	// A chart that does need action must say so, and say what to do.
	refused := crossCloud(t, "EC2", "aws", "DRP", "digitalocean", nil)
	w := refused.Warnings()
	if len(w) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(w), w)
	}
	if !strings.Contains(w[0], "no rule generated") || !strings.Contains(w[0], "Static public IP") {
		t.Errorf("warning does not state the problem and the fix: %q", w[0])
	}
}

// A rule with no port generates nothing and says why. Anything else would be
// fail-open: an abandoned half-written rule opening every port.
func TestUnsetPortIsRefused(t *testing.T) {
	s := model.Seed()
	s.Connections[0].AToB[0].Port = ""
	if err := model.Validate(&s); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	model.Normalise(&s)

	r := Classify(s)
	got := find(t, r, "Internet", "Edge CDN")[0]
	if got.Strategy != StratRefused {
		t.Fatalf("got %s, want refused", got.Strategy)
	}
	if !strings.Contains(got.Reason, `"*"`) {
		t.Errorf("refusal does not say how to allow all ports: %q", got.Reason)
	}
	// A refusal the user never sees is the same failure as a silently open rule.
	warnings := r.Warnings()
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "no port set") {
		t.Errorf("unset port did not reach the warnings: %v", warnings)
	}
}
