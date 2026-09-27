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

// publicAccess pins a distribution to the public path. The default is a VPC
// origin, so a case about a public address has to say so or it is testing the
// other branch.
func publicAccess() map[string]any {
	return map[string]any{catalog.ParamOriginAccess: catalog.OriginAccessPublic}
}

func originOf(t *testing.T, s model.Session) wiring {
	t.Helper()
	model.Normalise(&s)
	return originWiring(s)["edge"]
}

// An instance whose address moves is not an origin. The refusal has to name the
// toggle that fixes it, or the user is told no and not what to do.
func TestEphemeralInstanceIsNotAnOrigin(t *testing.T) {
	got := originOf(t, originChart("aws", "CDN", "EC2", "443", publicAccess(), nil)).reason
	if got == "" {
		t.Fatal("an instance with a moving address was accepted as an origin")
	}
	if !strings.Contains(got, "Static public IP") {
		t.Errorf("refusal does not name the field that fixes it: %s", got)
	}
}

// With the toggle on, the origin is the durable name and not the instance's own.
func TestStaticInstanceOriginUsesTheElasticAddress(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "EC2", "443", publicAccess(),
		map[string]any{catalog.ParamStaticPublicIP: true}))
	if w.reason != "" {
		t.Fatalf("refused an instance with a durable address: %s", w.reason)
	}
	if got := fixedExpr(w, "origin", "domain_name"); got != "aws_eip.origin.public_dns" {
		t.Errorf("origin domain is %q, want the elastic address", got)
	}
}

func TestLoadBalancerOriginUsesItsDNSName(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "ALB", "8080", publicAccess(), nil))
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

// Both ports reach the arguments that carry them. Each is asserted on its own,
// so re-hardcoding either fails here rather than passing because a default
// happened to match.
func TestOriginPortsReachTheOriginConfig(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "ALB", "8080",
		map[string]any{
			catalog.ParamOriginProtocol:  "http-only",
			catalog.ParamOriginAccess:    catalog.OriginAccessPublic,
			catalog.ParamOriginHTTPPort:  "8080",
			catalog.ParamOriginHTTPSPort: "8443",
		}, nil))
	if got := fixedExpr(w, "origin.custom_origin_config", "http_port"); got != "8080" {
		t.Errorf("http_port is %q, want the port set on the distribution", got)
	}
	if got := fixedExpr(w, "origin.custom_origin_config", "https_port"); got != "8443" {
		t.Errorf("https_port is %q, want the port set on the distribution", got)
	}
}

// The defect this split exists for. Under match-viewer CloudFront connects on
// whichever port matches the viewer, so both are live — and while one drawn port
// filled one of them, the other kept a default nothing had opened.
func TestMatchViewerUsesBothPorts(t *testing.T) {
	edge := map[string]any{
		catalog.ParamOriginProtocol:  "match-viewer",
		catalog.ParamOriginHTTPPort:  "8080",
		catalog.ParamOriginHTTPSPort: "8443",
	}
	s := originChart("aws", "CDN", "ALB", "8443", edge, nil)
	model.Normalise(&s)

	vpc := companionNamed(t, originWiring(s)["edge"], "aws_cloudfront_vpc_origin")
	if got := companionExpr(vpc, "vpc_origin_endpoint_config", "http_port"); got != "8080" {
		t.Errorf("http_port is %q, want the port set on the distribution", got)
	}

	// The line permits 8443 and not 8080, so the plaintext half would be refused
	// by the security group while every file in the output looks correct.
	got := joined(originWarnings(s, originWiring(s)))
	if !strings.Contains(got, "8080") {
		t.Errorf("no warning names the port the line does not permit:\n%s", got)
	}
	if strings.Contains(got, "8443") {
		t.Errorf("warned about a port the line does permit:\n%s", got)
	}
}

// A port the distribution fetches on that no rule allows is a closed door, and
// nothing else in the output says so.
func TestUnpermittedOriginPortWarns(t *testing.T) {
	s := originChart("aws", "CDN", "ALB", "443",
		map[string]any{catalog.ParamOriginHTTPSPort: "8443"}, nil)
	model.Normalise(&s)
	if got := joined(originWarnings(s, originWiring(s))); !strings.Contains(got, "8443") {
		t.Errorf("fetching on an unopened port produced no warning:\n%s", got)
	}
}

// The ordinary case stays quiet, or the warning is noise and gets ignored.
func TestPermittedOriginPortIsSilent(t *testing.T) {
	s := originChart("aws", "CDN", "ALB", "443", nil, nil)
	model.Normalise(&s)
	if got := joined(originWarnings(s, originWiring(s))); strings.Contains(got, "permits no rule") {
		t.Errorf("a matching port warned anyway:\n%s", got)
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
	// GCP, because a Google backend still takes its port from the line: the port
	// becomes a named_port and a health check, and neither can be a range.
	for _, port := range []string{"", "*", "8000-8080"} {
		s := originChart("gcp", "GLB", "GCE", port, nil, nil)
		if got := originOf(t, s).reason; got == "" {
			t.Errorf("port %q was accepted as an origin port", port)
		}
	}
}

// A refusal still has to leave configuration that plans. A distribution with no
// origin block at all is not valid HCL for CloudFront, so the fallback survives
// alongside the reason.
func TestRefusedOriginStillLeavesADistribution(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "EC2", "443", publicAccess(), nil))
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
	s := originChart("aws", "CDN", "EC2", "443", publicAccess(), nil)
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

// The point of the whole feature: an instance with no public address of any kind
// is a valid origin, because CloudFront reaches it from inside the VPC.
func TestPrivateInstanceIsAVPCOrigin(t *testing.T) {
	s := originChart("aws", "CDN", "EC2", "443", nil, nil)
	model.Normalise(&s)
	// Asserted rather than assumed: if either default ever flips, this case would
	// otherwise keep passing while testing a public instance.
	origin := s.Accounts[0].Assets[1].Params
	if origin[catalog.ParamStaticPublicIP] != false || origin["associate_public_ip_address"] != false {
		t.Fatalf("the instance under test has a public address: %v", origin)
	}

	w := originWiring(s)["edge"]
	if w.reason != "" {
		t.Fatalf("refused an instance with no public address: %s", w.reason)
	}
	if got := fixedExpr(w, "origin", "domain_name"); got != "aws_instance.origin.private_dns" {
		t.Errorf("origin domain is %q, want the instance's private name", got)
	}
	if got := fixedExpr(w, "origin.custom_origin_config", "http_port"); got != "" {
		t.Errorf("a VPC origin also emitted custom_origin_config (%q) — the two are alternatives", got)
	}
	if got := fixedExpr(w, "origin.vpc_origin_config", "vpc_origin_id"); got != "aws_cloudfront_vpc_origin.{{asset}}_vpc_origin.id" {
		t.Errorf("vpc_origin_id is %q", got)
	}
	vpc := companionNamed(t, w, "aws_cloudfront_vpc_origin")
	if got := companionExpr(vpc, "vpc_origin_endpoint_config", "arn"); got != "aws_instance.origin.arn" {
		t.Errorf("the VPC origin points at %q, want the instance it fronts", got)
	}
}

func TestLoadBalancerIsAVPCOrigin(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "ALB", "8080", nil, nil))
	if w.reason != "" {
		t.Fatalf("refused a load balancer as a VPC origin: %s", w.reason)
	}
	vpc := companionNamed(t, w, "aws_cloudfront_vpc_origin")
	if got := companionExpr(vpc, "vpc_origin_endpoint_config", "arn"); got != "aws_lb.origin.arn" {
		t.Errorf("the VPC origin points at %q, want the load balancer", got)
	}
}

// A function URL is not in the VPC, so it is refused by name rather than quietly
// served over the public path — a security setting that silently does the other
// thing is the failure this tool exists to avoid.
func TestFunctionURLCannotBeAVPCOrigin(t *testing.T) {
	got := originOf(t, originChart("aws", "CDN", "λ", "443", nil,
		map[string]any{catalog.ParamFunctionURL: true})).reason
	if got == "" {
		t.Fatal("a function URL was accepted as a VPC origin")
	}
	if !strings.Contains(got, "Origin access") || !strings.Contains(got, catalog.OriginAccessPublic) {
		t.Errorf("refusal does not name the switch that fixes it: %s", got)
	}
}

// The ports have to reach the VPC origin too. They are on the companion rather
// than the distribution, which is exactly how a port gets hardcoded and nobody
// notices.
func TestOriginPortsReachTheVPCOrigin(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "ALB", "8080",
		map[string]any{
			catalog.ParamOriginProtocol:  "http-only",
			catalog.ParamOriginHTTPPort:  "8080",
			catalog.ParamOriginHTTPSPort: "8443",
		}, nil))
	vpc := companionNamed(t, w, "aws_cloudfront_vpc_origin")
	if got := companionExpr(vpc, "vpc_origin_endpoint_config", "http_port"); got != "8080" {
		t.Errorf("http_port is %q, want the port set on the distribution", got)
	}
	if got := companionExpr(vpc, "vpc_origin_endpoint_config", "https_port"); got != "8443" {
		t.Errorf("https_port is %q, want the port set on the distribution", got)
	}
}

// A private origin still needs the managed prefix list: AWS admits a VPC origin
// either by that list or by a security group it creates afterwards, and only the
// first can be written before the VPC origin exists.
func TestVPCOriginStillGetsThePrefixListRule(t *testing.T) {
	s := originChart("aws", "CDN", "EC2", "443", nil, nil)
	model.Normalise(&s)
	f, ok := Classify(s).FlowFor("conn", model.NodeRef{Type: model.NodeAsset, AccountID: "acc", AssetID: "edge"})
	if !ok {
		t.Fatal("no flow for the edge-to-origin direction")
	}
	if f.Outcomes[0].Strategy != StratEdgeNative {
		t.Fatalf("strategy is %q, want the native edge construct", f.Outcomes[0].Strategy)
	}
	if !strings.Contains(f.Outcomes[0].Source, "cloudfront.origin-facing") {
		t.Errorf("source is %q, want the CloudFront managed prefix list", f.Outcomes[0].Source)
	}
}

// Detaching is the only way AWS allows a VPC origin to be edited or deleted, so
// the resource has to survive the disassociation — a state that dropped both
// would be the same dead end the user started in.
func TestDetachedKeepsTheVPCOriginAndDropsTheAssociation(t *testing.T) {
	w := originOf(t, originChart("aws", "CDN", "EC2", "443",
		map[string]any{catalog.ParamDetachVPCOrigin: true}, nil))
	if w.reason != "" {
		t.Fatalf("detaching refused the origin outright: %s", w.reason)
	}
	companionNamed(t, w, "aws_cloudfront_vpc_origin")
	if got := fixedExpr(w, "origin.vpc_origin_config", "vpc_origin_id"); got != "" {
		t.Errorf("the distribution still names the VPC origin (%q), so CloudFront would still refuse to free it", got)
	}
	if got := fixedExpr(w, "origin", "domain_name"); !strings.HasPrefix(got, "var.") {
		t.Errorf("a detached distribution fetches from %q, want the placeholder", got)
	}
	if len(w.vars) == 0 {
		t.Error("the placeholder variable is not declared, so the chart would not plan")
	}
}

// Detached is a maintenance state, not a resting one: the chart says the CDN
// fronts the instance and the configuration fetches from example.com.
func TestDetachedWarnsOnTheNode(t *testing.T) {
	s := originChart("aws", "CDN", "EC2", "443",
		map[string]any{catalog.ParamDetachVPCOrigin: true}, nil)
	model.Normalise(&s)
	got := joined(originWarnings(s, originWiring(s)))
	if !strings.Contains(got, "detached") {
		t.Fatalf("a detached distribution produced no warning:\n%s", got)
	}
	if !strings.Contains(got, "Detach VPC origin") {
		t.Errorf("the warning does not name the toggle that clears it:\n%s", got)
	}
}

// The toggle means nothing on the public path, and a control that appears where
// it does nothing is one the user will try.
func TestDetachIsGatedOnTheVPCPath(t *testing.T) {
	rt, ok := catalog.Type("aws", "CDN")
	if !ok {
		t.Fatal("no CloudFront type")
	}
	for _, f := range rt.Params {
		if f.Key != catalog.ParamDetachVPCOrigin {
			continue
		}
		if f.RequiresParam != catalog.ParamOriginAccess || f.RequiresValue != catalog.OriginAccessVPC {
			t.Errorf("detach is gated on %q=%q, want the VPC path", f.RequiresParam, f.RequiresValue)
		}
		return
	}
	t.Fatal("no detach field on the CloudFront type")
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
