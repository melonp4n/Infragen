package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// originChart builds one account with an edge in front of one origin, so each
// case below differs only in what it is meant to differ in.
func originChart(provider, edgeCode, originCode, port string, edgeParams, originParams map[string]any) model.Session {
	build := func(id, code string, overrides map[string]any) model.Asset {
		params := catalog.Defaults(provider, code)
		for k, v := range overrides {
			params[k] = v
		}
		return model.Asset{ID: id, Code: code, Name: id, Params: params}
	}
	rules := []model.Rule{{Protocol: "TCP", Port: port, Detail: "origin fetch"}}
	if port == "" {
		rules = nil
	}
	return model.Session{
		Version: model.SchemaVersion,
		Accounts: []model.Account{{
			ID: "acc", Name: "Account", Provider: provider, Params: catalog.AccountDefaults(provider),
			Assets: []model.Asset{
				build("edge", edgeCode, edgeParams),
				build("origin", originCode, originParams),
			},
		}},
		Connections: []model.Connection{{
			ID:   "conn",
			A:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc", AssetID: "edge"},
			B:    model.NodeRef{Type: model.NodeAsset, AccountID: "acc", AssetID: "origin"},
			AToB: rules,
		}},
	}
}

func originOf(t *testing.T, s model.Session) wiring {
	t.Helper()
	model.Normalise(&s)
	return originWiring(s)["edge"]
}

// An instance whose address moves is not an origin. The refusal has to name the
// toggle that fixes it, or the user is told no and not what to do.
func TestEphemeralInstanceIsNotAnOrigin(t *testing.T) {
	got := originOf(t, originChart("aws", "CDN", "EC2", "443", nil, nil)).reason
	if got == "" {
		t.Fatal("an instance with a moving address was accepted as an origin")
	}
	if !strings.Contains(got, "Static public IP") {
		t.Errorf("refusal does not name the field that fixes it: %s", got)
	}
}

// With the toggle on, the origin is the durable name and not the instance's own.
func TestStaticInstanceOriginUsesTheElasticAddress(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "EC2", "443", nil,
		map[string]any{catalog.ParamStaticPublicIP: true}))
	if w.reason != "" {
		t.Fatalf("refused an instance with a durable address: %s", w.reason)
	}
	if got := fixedExpr(w, "origin", "domain_name"); got != "aws_eip.origin.public_dns" {
		t.Errorf("origin domain is %q, want the elastic address", got)
	}
}

func TestLoadBalancerOriginUsesItsDNSName(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "ALB", "8080", nil, nil))
	if w.reason != "" {
		t.Fatalf("refused a load balancer origin: %s", w.reason)
	}
	if got := fixedExpr(w, "origin", "domain_name"); got != "aws_lb.origin.dns_name" {
		t.Errorf("origin domain is %q, want the load balancer name", got)
	}
}

// A database answers no HTTP request, so nothing can be put in front of it.
func TestDatabaseCannotBeAnOrigin(t *testing.T) {
	if got := originOf(t, originChart("aws", "CDN", "RDS", "443", nil, nil)).reason; got == "" {
		t.Fatal("a managed database was accepted as an origin")
	}
}

// The port reaches every argument that has to carry it. Each is asserted on its
// own, so re-hardcoding any one of them fails here rather than passing because
// the default happened to match.
func TestDrawnPortReachesTheOriginPort(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "ALB", "8080",
		map[string]any{catalog.ParamOriginProtocol: "http-only"}, nil))
	if got := fixedExpr(w, "origin.custom_origin_config", "http_port"); got != "8080" {
		t.Errorf("http_port is %q, want the drawn port", got)
	}
	if got := fixedExpr(w, "origin.custom_origin_config", "https_port"); got != "443" {
		t.Errorf("https_port is %q, want the untouched default", got)
	}
}

func TestDrawnPortReachesEveryGoogleBackendArgument(t *testing.T) {
	w := originOf(t, originChart("gcp", "GLB", "GCE", "8080", nil, nil))
	if w.reason != "" {
		t.Fatalf("refused a Compute Engine origin: %s", w.reason)
	}
	group := companionNamed(t, w, "google_compute_instance_group")
	if got := companionExpr(group, "named_port", "port"); got != "8080" {
		t.Errorf("named port is %q, want the drawn port", got)
	}
	check := companionNamed(t, w, "google_compute_health_check")
	if got := companionExpr(check, "http_health_check", "port"); got != "8080" {
		t.Errorf("health check port is %q — a check on the wrong port fails every probe", got)
	}
}

// Cloud CDN in front of a bucket cannot reach an instance, and the refusal has to
// say what can.
func TestBackendBucketCannotFrontAnInstance(t *testing.T) {
	got := originOf(t, originChart("gcp", "CDN", "GCE", "8080", nil, nil)).reason
	if got == "" {
		t.Fatal("a backend bucket was accepted in front of an instance")
	}
	if !strings.Contains(got, "load balancer") {
		t.Errorf("refusal does not name the fix: %s", got)
	}
}

// An origin is fetched on one port, so neither a wildcard nor a range names one,
// and a blank port is unspecified rather than 80.
func TestOriginPortMustBeASinglePort(t *testing.T) {
	for _, port := range []string{"", "*", "8000-8080"} {
		s := originChart("aws", "CDN", "ALB", port, nil, nil)
		if got := originOf(t, s).reason; got == "" {
			t.Errorf("port %q was accepted as an origin port", port)
		}
	}
}

// A refusal still has to leave configuration that plans. A distribution with no
// origin block at all is not valid HCL for CloudFront, so the fallback survives
// alongside the reason.
func TestRefusedOriginStillLeavesADistribution(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "EC2", "443", nil, nil))
	if w.reason == "" {
		t.Fatal("expected a refusal")
	}
	if got := fixedExpr(w, "origin", "domain_name"); !strings.HasPrefix(got, "var.") {
		t.Errorf("a refused origin left domain_name as %q, so the chart would not plan", got)
	}
}

// Nothing drawn behind a distribution is not an error, but it is a placeholder,
// and the user has to be told rather than left to discover it at apply.
func TestUnconnectedEdgeWarns(t *testing.T) {
	s := model.EveryType()
	model.Normalise(&s)
	warnings := originWarnings(s, originWiring(s))
	if len(warnings) == 0 {
		t.Fatal("a chart of edges with no origins produced no warning")
	}
	if !strings.Contains(joined(warnings), "placeholder") {
		t.Errorf("warnings do not say the origin is a placeholder:\n%s", joined(warnings))
	}
}

// The drawer reads Classify, not the wiring map, so a refusal that never reaches
// an Outcome is a refusal the user only meets after pressing Generate.
func TestRefusedOriginShowsOnTheRuleRow(t *testing.T) {
	s := originChart("aws", "CDN", "EC2", "443", nil, nil)
	model.Normalise(&s)
	f, ok := Classify(s).FlowFor("conn", model.NodeRef{Type: model.NodeAsset, AccountID: "acc", AssetID: "edge"})
	if !ok {
		t.Fatal("no flow for the edge-to-origin direction")
	}
	if f.Outcomes[0].Strategy != StratRefused {
		t.Fatalf("strategy is %q, want a refusal — the rule row would say a rule will be generated",
			f.Outcomes[0].Strategy)
	}
	if !f.Outcomes[0].NeedsAction() {
		t.Error("the refusal is not marked as needing action, so the drawer would render it as information")
	}
}

// The defect this file exists for.
//
// Which of a connection's two directions is "A to B" is decided by whichever
// connector was clicked first, and the drawer renders both identically. A rule in
// the other one used to be reported as "has no rule on it" — a statement the user
// could see was false, which is worse than silence.
func TestRuleOnTheWrongDirectionSaysSo(t *testing.T) {
	s := originChart("gcp", "GLB", "GCE", "8080", nil, nil)
	// Same rule, other list. Nothing else changes.
	s.Connections[0].BToA = s.Connections[0].AToB
	s.Connections[0].AToB = nil
	model.Normalise(&s)

	got := originWiring(s)["edge"].reason
	if got == "" {
		t.Fatal("a rule in the other direction produced no refusal, so an empty load balancer would generate silently")
	}
	if strings.Contains(got, "no rule on this line") {
		t.Errorf("still claims the line has no rule when it has one: %s", got)
	}
	// Both directions named in full, so there is no guessing which list to use.
	for _, want := range []string{"Account / origin → Account / edge", "Account / edge → Account / origin"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not name %q: %s", want, got)
		}
	}

	// And it reaches the drawer, on the direction the rule has to move to.
	if note := OriginNote(s, "conn", "aToB"); note != got {
		t.Errorf("drawer note on the edge→origin direction is %q, want the refusal", note)
	}
	if note := OriginNote(s, "conn", "bToA"); note != "" {
		t.Errorf("the direction the rule is already on carries a note: %q", note)
	}
}

func TestNoRuleEitherWaySaysWhatToAdd(t *testing.T) {
	s := originChart("gcp", "GLB", "GCE", "", nil, nil)
	got := originOf(t, s).reason
	if !strings.Contains(got, "no rule on this line") {
		t.Errorf("a line with no rules at all does not say so: %s", got)
	}
	if !strings.Contains(got, "Account / edge → Account / origin") {
		t.Errorf("does not name the direction to add the rule to: %s", got)
	}
}

// A warning with no ID cannot be drawn on the canvas or clicked in the panel, so
// it is back to being a name to hunt for.
func TestEveryOriginWarningNamesItsAsset(t *testing.T) {
	s := originChart("gcp", "GLB", "GCE", "8080", nil, nil)
	s.Connections[0].BToA = s.Connections[0].AToB
	s.Connections[0].AToB = nil
	model.Normalise(&s)

	ws := originWarnings(s, originWiring(s))
	if len(ws) == 0 {
		t.Fatal("no warning for a load balancer that will have no backend")
	}
	for _, w := range ws {
		if w.AssetID == "" {
			t.Errorf("warning has no asset to mark on the chart: %q", w.Text)
		}
		if w.Severity != SevError {
			t.Errorf("a load balancer that will not serve is not an error: %q", w.Text)
		}
	}
	if !Errors(ws) {
		t.Error("Errors reported none, so the panel would not lead with the problem")
	}
}

// A user may type {{param:x}} into a name. Expansion must not read it back.
func TestParamPlaceholderIsNotRescanned(t *testing.T) {
	got := expandParams("{{param:a}}", map[string]any{"a": "{{param:b}}", "b": "leaked"})
	if strings.Contains(got, "leaked") {
		t.Errorf("a parameter value was expanded as a placeholder: %s", got)
	}
}

func fixedExpr(w wiring, block, key string) string {
	for _, f := range w.fixed {
		if f.Block == block && f.Key == key {
			return f.Expr
		}
	}
	return ""
}

func companionNamed(t *testing.T, w wiring, tofuType string) catalog.Companion {
	t.Helper()
	for _, c := range w.companions {
		if c.TofuType == tofuType {
			return c
		}
	}
	t.Fatalf("no %s was emitted", tofuType)
	return catalog.Companion{}
}

func companionExpr(c catalog.Companion, block, key string) string {
	for _, f := range c.Fixed {
		if f.Block == block && f.Key == key {
			return f.Expr
		}
	}
	return ""
}
