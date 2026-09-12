package tofu

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func generate(t *testing.T, s model.Session) (string, []string) {
	t.Helper()
	if err := model.Validate(&s); err != nil {
		t.Fatalf("session invalid: %v", err)
	}
	model.Normalise(&s)
	return Generate(s)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — run: go test ./internal/tofu -update", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s. Run: go test ./internal/tofu -update", path)
	}
}

func TestGenerateSeed(t *testing.T) {
	hcl, _ := generate(t, model.Seed())
	golden(t, "seed.tf", hcl)
}

// A security group that is never attached to anything protects nothing, so the
// output would look complete and do nothing.
func TestGuardedAssetsAreAttached(t *testing.T) {
	hcl, _ := generate(t, model.Seed())

	flat := collapse(hcl)
	// The EC2 instance carries rules in the seed chart, so its group must be
	// referenced from the instance itself.
	if !strings.Contains(flat, "vpc_security_group_ids = [aws_security_group.asset_3.id]") {
		t.Error("EC2 instance does not reference its own security group")
	}
	// The droplet is attached by the firewall resource naming it.
	if !strings.Contains(flat, "droplet_ids = [digitalocean_droplet.asset_7.id]") {
		t.Error("DigitalOcean firewall does not name the droplet it protects")
	}
	// Anything that could not be attached must say so rather than look done.
	for _, group := range securityGroups(hcl) {
		if !strings.Contains(hcl, group+".id]") && !strings.Contains(hcl, "attach its firewall yourself") {
			t.Errorf("%s is created but never attached and never flagged", group)
		}
	}
}

func securityGroups(hcl string) []string {
	var out []string
	for _, line := range strings.Split(hcl, "\n") {
		if strings.HasPrefix(line, `resource "aws_security_group" "`) {
			name := strings.Trim(strings.Fields(line)[2], `"`)
			out = append(out, "aws_security_group."+name)
		}
	}
	return out
}

// A service endpoint has no firewall, so it must never appear in a rule. This is
// the regression most likely to creep back in, because it looks like a gap.
func TestNoRulesForServiceEndpoints(t *testing.T) {
	s := model.Seed()
	hcl, _ := generate(t, s)

	var serviceAssets []string
	for _, acc := range s.Accounts {
		for _, a := range acc.Assets {
			if rt, ok := catalog.Type(acc.Provider, a.Code); ok && rt.Network == catalog.NetServiceEndpoint {
				serviceAssets = append(serviceAssets, a.ID)
			}
		}
	}
	if len(serviceAssets) == 0 {
		t.Fatal("seed chart has no service endpoints, so this test proved nothing")
	}

	for _, id := range serviceAssets {
		for _, bad := range []string{
			`resource "aws_security_group" "` + id + `"`,
			`resource "digitalocean_firewall" "` + id + `"`,
			`resource "azurerm_network_security_group" "` + id + `"`,
			"security_group_id = aws_security_group." + id + ".id",
		} {
			if strings.Contains(hcl, bad) {
				t.Errorf("service endpoint %s got a firewall: %q", id, bad)
			}
		}
	}
}

// Renaming an asset must not move a single resource address, or `apply` would
// destroy and recreate infrastructure over a label change.
func TestRenameDoesNotMoveResources(t *testing.T) {
	before, _ := generate(t, model.Seed())

	renamed := model.Seed()
	renamed.Accounts[0].Assets[2].Name = "Totally Different Name"
	renamed.Accounts[0].Name = "Renamed Account"
	after, _ := generate(t, renamed)

	if addresses(before) != addresses(after) {
		t.Error("renaming an asset changed a resource address — apply would destroy and recreate it")
	}
	if before == after {
		t.Error("rename changed nothing at all, so this test proved nothing")
	}
}

// addresses reduces the output to its resource and data block addresses, which are
// what OpenTofu tracks identity by.
func addresses(hcl string) string {
	var out []string
	for _, line := range strings.Split(hcl, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "resource ") || strings.HasPrefix(trimmed, "data ") {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n")
}

// Swapping one asset for another must touch only that asset and the rules that
// referenced it.
func TestSwapTouchesOnlyTheSwappedAsset(t *testing.T) {
	before, _ := generate(t, model.Seed())

	swapped := model.Seed()
	// Drop the droplet and its connection, and add an Azure VM in its place.
	do := &swapped.Accounts[2]
	droplet := do.Assets[0].ID
	do.Assets = do.Assets[1:]
	var kept []model.Connection
	for _, c := range swapped.Connections {
		if c.A.AssetID == droplet || c.B.AssetID == droplet {
			continue
		}
		kept = append(kept, c)
	}
	swapped.Connections = kept
	az := &swapped.Accounts[3]
	az.Assets = append(az.Assets, model.Asset{
		ID: "asset_new", Code: "VM", Name: "Replacement worker",
		Params: catalog.Defaults("azure", "VM"),
	})
	after, _ := generate(t, swapped)

	gone, added := diffAddresses(addresses(before), addresses(after))
	for _, line := range gone {
		if !strings.Contains(line, droplet) {
			t.Errorf("swap removed an unrelated resource: %s", line)
		}
	}
	for _, line := range added {
		if !strings.Contains(line, "asset_new") {
			t.Errorf("swap added an unrelated resource: %s", line)
		}
	}
	if len(gone) == 0 || len(added) == 0 {
		t.Fatal("swap changed nothing, so this test proved nothing")
	}
}

func diffAddresses(before, after string) (gone, added []string) {
	inAfter := map[string]bool{}
	for _, l := range strings.Split(after, "\n") {
		inAfter[l] = true
	}
	inBefore := map[string]bool{}
	for _, l := range strings.Split(before, "\n") {
		inBefore[l] = true
		if !inAfter[l] {
			gone = append(gone, l)
		}
	}
	for _, l := range strings.Split(after, "\n") {
		if !inBefore[l] {
			added = append(added, l)
		}
	}
	return gone, added
}

// Generation must be a pure function of the session, or golden tests and diffs
// between two runs are both meaningless.
func TestGenerationIsDeterministic(t *testing.T) {
	first, _ := generate(t, model.Seed())
	for i := 0; i < 5; i++ {
		again, _ := generate(t, model.Seed())
		if first != again {
			t.Fatal("two generations of the same chart differ")
		}
	}
}

// A display name reaching an HCL expression unescaped would be evaluated as an
// interpolation. Comments are exempt: HCL does not interpolate them.
func TestUserStringsCannotInject(t *testing.T) {
	s := model.Seed()
	s.Accounts[0].Assets[2].Name = `${file("/etc/passwd")} and "quotes"`
	hcl, _ := generate(t, s)

	escaped := false
	for _, line := range strings.Split(hcl, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, `$${file(`) {
			escaped = true
		}
		// Remove the escaped form before looking for a live one: `$${` contains
		// `${` as a substring, so a naive check can never distinguish them.
		if strings.Contains(strings.ReplaceAll(line, `$${`, ""), `${file(`) {
			t.Errorf("a display name reached an expression as a live interpolation: %s", line)
		}
	}
	if !escaped {
		t.Error("interpolation was never escaped to a literal, so this test proved nothing")
	}
}

// collapse squashes runs of spaces so assertions do not depend on the alignment
// padding the writer adds.
func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " \n ")), " ")
}

// The static address toggle is what makes a cross-account rule possible, so it
// must produce both the address resource and its association.
func TestStaticAddressEmitsResourceAndAssociation(t *testing.T) {
	s := model.Seed()
	s.Accounts[0].Assets[2].Params[catalog.ParamStaticPublicIP] = true
	hcl, _ := generate(t, s)

	for _, want := range []string{
		`resource "aws_eip" "asset_3"`,
		`resource "aws_eip_association" "asset_3"`,
		"allocation_id = aws_eip.asset_3.id",
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// Without the toggle, nothing extra is provisioned — the user did not ask for it.
func TestNoStaticAddressWithoutTheToggle(t *testing.T) {
	hcl, _ := generate(t, model.Seed())
	if strings.Contains(hcl, `resource "aws_eip"`) {
		t.Error("provisioned an elastic IP the user did not enable")
	}
}

// Generated output must parse as HCL and already be canonically formatted. If it
// is not, the first `tofu fmt` a user runs produces a large diff against every
// later generation, which ruins the repeated-apply workflow.
//
// Skipped when neither binary is installed; this is a check worth having in CI.
func TestGeneratedHCLIsValidAndFormatted(t *testing.T) {
	bin := ""
	for _, candidate := range []string{"tofu", "terraform"} {
		if path, err := exec.LookPath(candidate); err == nil {
			bin = path
			break
		}
	}
	if bin == "" {
		t.Skip("neither tofu nor terraform is installed")
	}

	hcl, _ := generate(t, model.Seed())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatal(err)
	}

	// `fmt -check` fails on a parse error as well as on formatting, so it covers
	// both without needing network access for a provider download.
	out, err := exec.Command(bin, "fmt", "-check", "-diff", dir).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("generated HCL is not valid canonical HCL:\n%s", out)
	}
}

// The acceptance gate for the required-argument work: every registered type in one
// chart, written out for `terraform validate` to check against real provider
// schemas. Parsing is not validation — `fmt` says nothing about whether an
// argument exists, which is exactly how the invalid parameters went unnoticed.
func TestWriteEveryTypeFixture(t *testing.T) {
	hcl, _ := generate(t, model.EveryType())
	golden(t, "everytype.tf", hcl)
}

// An all-ports TCP rule must still carry a range. AWS rejects a tcp rule with no
// ports at apply time, and `validate` does not catch it because the schema marks
// from_port and to_port optional — so only a test stops this regressing.
func TestAllPortsRuleCarriesAnExplicitRange(t *testing.T) {
	s := model.Seed()
	// The reported case: allow everything outbound to the internet.
	for i := range s.Connections {
		if s.Connections[i].ID == "conn_5" {
			s.Connections[i].AToB[0].Port = "*"
		}
	}
	hcl, _ := generate(t, s)
	flat := collapse(hcl)

	if !strings.Contains(flat, "from_port = 0") || !strings.Contains(flat, "to_port = 65535") {
		t.Errorf("all-ports TCP rule has no port range:\n%s", hcl)
	}
	// ICMP and ALL take no ports, so the range must not be added blindly.
	if strings.Contains(flat, `ip_protocol = "-1" from_port`) {
		t.Error("a protocol-any rule was given a port range")
	}
}

// Nothing at all should be emitted for a rule with no port — not a permissive
// fallback, not a rule with an empty range.
func TestUnsetPortEmitsNoRule(t *testing.T) {
	s := model.Seed()
	// The jump host's SSH rule, so the asset still exists and only this goes away.
	for i := range s.Connections {
		if s.Connections[i].ID == "conn_7" {
			s.Connections[i].AToB[0].Port = ""
		}
	}
	hcl, warnings := generate(t, s)

	if strings.Contains(hcl, "asset_3_ingress_tcp_22_22") {
		t.Error("a rule was generated for a port that was never set")
	}
	if strings.Contains(collapse(hcl), "from_port = 0 to_port = 65535") {
		t.Error("an unset port fell back to allowing every port")
	}
	if len(warnings) == 0 {
		t.Error("no warning for the rule that was dropped")
	}
}

// Ports describe the destination in both directions, and source ports are never
// constrained. AWS's from_port/to_port naming reads like source-and-destination,
// so this pins the actual behaviour.
func TestPortsAreDestinationOnly(t *testing.T) {
	hcl, _ := generate(t, model.Seed())
	flat := collapse(hcl)

	// The ALB reaches the web tier on 8080. Both the egress rule on the source and
	// the ingress rule on the destination carry 8080 — the destination port.
	for _, want := range []string{
		`resource "aws_vpc_security_group_egress_rule" "asset_2_egress_tcp_8080_8080_1" { provider = aws.acc_1 security_group_id = aws_security_group.asset_2.id ip_protocol = "tcp" from_port = 8080 to_port = 8080`,
		`resource "aws_vpc_security_group_ingress_rule" "asset_3_ingress_tcp_8080_8080_0" { provider = aws.acc_1 security_group_id = aws_security_group.asset_3.id ip_protocol = "tcp" from_port = 8080 to_port = 8080`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing rule:\n%s", want)
		}
	}

	// Azure is the one provider with a distinct source-port field, and it must
	// stay unconstrained.
	if strings.Contains(hcl, "azurerm_network_security_rule") &&
		!strings.Contains(flat, `source_port_range = "*"`) {
		t.Error("Azure rule constrains the source port")
	}
}

// A multi-line startup script must survive into valid HCL. This is the regression
// the newline escaping exists for, and each provider spells the argument
// differently — Azure additionally needs it base64-encoded.
func TestUserDataPerProvider(t *testing.T) {
	script := "#!/bin/bash\nset -euo pipefail\napt-get update\n"
	s := model.Seed()
	set := func(accIndex, assetIndex int, key string) {
		s.Accounts[accIndex].Assets[assetIndex].Params[key] = script
	}
	set(0, 2, "user_data")   // AWS EC2
	set(2, 0, "user_data")   // DigitalOcean droplet
	set(3, 0, "custom_data") // Azure VM
	hcl, _ := generate(t, s)

	for _, want := range []string{
		`user_data = "#!/bin/bash\nset -euo pipefail\napt-get update\n"`,
		`custom_data = base64encode("#!/bin/bash\nset -euo pipefail\napt-get update\n")`,
	} {
		if !strings.Contains(collapse(hcl), want) {
			t.Errorf("missing %s", want)
		}
	}
	// A literal newline in the output would be an HCL parse error.
	for _, line := range strings.Split(hcl, "\n") {
		if strings.Contains(line, "apt-get update") && !strings.Contains(line, `\n`) {
			t.Errorf("script was embedded rather than escaped: %q", line)
		}
	}
}

// An unwritten script is absence, not an empty one — and base64encode("") is
// worse than nothing.
func TestEmptyScriptIsOmitted(t *testing.T) {
	hcl, _ := generate(t, model.Seed())
	for _, bad := range []string{`user_data = ""`, `base64encode("")`, `metadata_startup_script = ""`} {
		if strings.Contains(collapse(hcl), bad) {
			t.Errorf("emitted %s for an unset script", bad)
		}
	}
}

// An instance-store or container-backed AMI has no root EBS volume, and Terraform
// rejects root_block_device on one. So the block has to be omitted entirely rather
// than emitted with sensible values.
func TestRootBlockDeviceIsOptional(t *testing.T) {
	ec2 := func(managed bool) string {
		s := model.Seed()
		a := &s.Accounts[0].Assets[2]
		if a.Code != "EC2" {
			t.Fatalf("fixture moved: expected EC2, got %q", a.Code)
		}
		a.Params[catalog.ParamRootBlockDevice] = managed
		hcl, _ := generate(t, s)
		i := strings.Index(hcl, `resource "aws_instance" "`+a.ID+`"`)
		if i < 0 {
			t.Fatalf("no aws_instance block for %s", a.ID)
		}
		return hcl[i : i+strings.Index(hcl[i:], "\n}\n")]
	}

	off := ec2(false)
	if strings.Contains(off, "root_block_device") {
		t.Errorf("emitted root_block_device with the toggle off:\n%s", off)
	}
	// The directive itself is not an argument on aws_instance either.
	if strings.Contains(off, catalog.ParamRootBlockDevice+" =") {
		t.Errorf("emitted the directive as an argument:\n%s", off)
	}

	on := ec2(true)
	for _, want := range []string{"root_block_device {", "volume_size = 20", "encrypted   = true"} {
		if !strings.Contains(collapse(on), collapse(want)) {
			t.Errorf("missing %q with the toggle on:\n%s", want, on)
		}
	}
}

// ec2Block generates the seed chart with the EC2's params tweaked and returns just
// that instance's resource block, so an assertion cannot accidentally match text
// belonging to another resource.
func ec2Block(t *testing.T, tweak func(*model.Asset)) (block, full string) {
	t.Helper()
	s := model.Seed()
	a := &s.Accounts[0].Assets[2]
	if a.Code != "EC2" {
		t.Fatalf("fixture moved: expected EC2, got %q", a.Code)
	}
	tweak(a)
	hcl, _ := generate(t, s)
	i := strings.Index(hcl, `resource "aws_instance" "`+a.ID+`"`)
	if i < 0 {
		t.Fatalf("no aws_instance block for %s", a.ID)
	}
	return hcl[i : i+strings.Index(hcl[i:], "\n}\n")], hcl
}

// A preset resolves through a published parameter where one exists. Nothing about
// the image name is assumed, which is the whole point: the first attempt guessed a
// name pattern and matched nothing.
func TestAMIPresetResolvesThroughParameter(t *testing.T) {
	block, full := ec2Block(t, func(a *model.Asset) {
		a.Params[catalog.ParamAMIOS] = "Amazon Linux 2023"
	})

	if !strings.Contains(collapse(block), collapse("ami = data.aws_ssm_parameter.asset_3_ami.value")) {
		t.Errorf("instance does not reference the parameter:\n%s", block)
	}
	for _, want := range []string{
		`data "aws_ssm_parameter" "asset_3_ami"`,
		`name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"`,
	} {
		if !strings.Contains(collapse(full), collapse(want)) {
			t.Errorf("missing %q in generated output", want)
		}
	}
	// A parameter is already the current image, so there is nothing to sort.
	if strings.Contains(full, "most_recent") {
		t.Error("emitted most_recent for a parameter lookup")
	}
}

// Debian publishes no parameter, so it is the one preset left matching on name.
// The owner must be Debian's own account: the wrong owner matches nothing, and
// nothing is what "Your query returned no results" means at apply.
func TestAMIPresetWithoutAParameterMatchesOnName(t *testing.T) {
	block, full := ec2Block(t, func(a *model.Asset) {
		a.Params[catalog.ParamAMIOS] = "Debian 12"
	})

	if !strings.Contains(collapse(block), collapse("ami = data.aws_ami.asset_3_ami.id")) {
		t.Errorf("instance does not reference the lookup:\n%s", block)
	}
	for _, want := range []string{
		`data "aws_ami" "asset_3_ami"`,
		`owners = ["136693071363"]`,
		`values = ["debian-12-amd64-*"]`,
		"most_recent = true",
	} {
		if !strings.Contains(collapse(full), collapse(want)) {
			t.Errorf("missing %q in generated output", want)
		}
	}
}

// The custom option is the escape hatch: the literal is used as typed, and no
// lookup is emitted for it.
func TestCustomAMIUsesTheLiteral(t *testing.T) {
	block, full := ec2Block(t, func(a *model.Asset) {
		a.Params[catalog.ParamAMIOS] = catalog.AMICustom
		a.Params["ami"] = "ami-0123456789abcdef0"
	})

	if !strings.Contains(collapse(block), collapse(`ami = "ami-0123456789abcdef0"`)) {
		t.Errorf("literal AMI not emitted:\n%s", block)
	}
	if strings.Contains(full, `data "aws_ami"`) {
		t.Errorf("emitted a lookup for a custom AMI:\n%s", full)
	}
}

// The two ways of setting ami must never both fire. OpenTofu rejects a repeated
// argument outright, so this would be a hard failure rather than a subtle one.
func TestAMIIsSetExactlyOnce(t *testing.T) {
	rt, ok := catalog.Type("aws", "EC2")
	if !ok {
		t.Fatal("no aws/EC2 in the catalog")
	}
	var options []string
	for _, f := range rt.Params {
		if f.Key == catalog.ParamAMIOS {
			options = f.Options
		}
	}
	if len(options) < 2 {
		t.Fatalf("expected the OS select to offer presets and a custom option, got %v", options)
	}
	for _, os := range options {
		block, _ := ec2Block(t, func(a *model.Asset) {
			a.Params[catalog.ParamAMIOS] = os
			a.Params["ami"] = "ami-0123456789abcdef0"
		})
		if n := strings.Count(block, "  ami "); n != 1 {
			t.Errorf("%s: ami set %d times, want exactly 1:\n%s", os, n, block)
		}
	}
}

// Choosing custom and typing nothing must be refused. Emitting ami = "" produces a
// resource that looks complete and cannot launch.
func TestBlankCustomAMIWarns(t *testing.T) {
	s := model.Seed()
	a := &s.Accounts[0].Assets[2]
	a.Params[catalog.ParamAMIOS] = catalog.AMICustom
	a.Params["ami"] = "   "

	_, warnings := generate(t, s)
	if !strings.Contains(joined(warnings), "left AMI ID empty") {
		t.Errorf("no warning for a blank custom AMI: %v", warnings)
	}
}

// The pin toggle has to coexist with the Ansible key precondition, because both
// live in the same lifecycle block and one overwriting the other would be silent.
func TestPinAMICoexistsWithPrecondition(t *testing.T) {
	block, _ := ec2Block(t, func(a *model.Asset) {
		a.Params[catalog.ParamPinAMI] = true
		a.Params[catalog.ParamAnsible] = true
		a.Params[catalog.ParamAnsibleGroup] = "web"
	})

	if !strings.Contains(collapse(block), collapse("ignore_changes = [ami]")) {
		t.Errorf("pin toggle did not emit ignore_changes:\n%s", block)
	}
	if !strings.Contains(block, "precondition {") {
		t.Errorf("key precondition lost when the pin toggle is on:\n%s", block)
	}
	if n := strings.Count(block, "lifecycle {"); n != 1 {
		t.Errorf("lifecycle block appears %d times, want 1:\n%s", n, block)
	}
}

// Off is the default, so an unpinned instance takes the newest matching image.
func TestPinAMIOffEmitsNothing(t *testing.T) {
	block, _ := ec2Block(t, func(a *model.Asset) {
		a.Params[catalog.ParamPinAMI] = false
	})
	if strings.Contains(block, "ignore_changes") {
		t.Errorf("emitted ignore_changes with the toggle off:\n%s", block)
	}
}

// internetEgress patches the seed chart's outbound-to-internet connection, which
// is the one that carries egress rules on the web tier EC2.
func internetEgress(t *testing.T, s *model.Session, rules ...model.Rule) {
	t.Helper()
	for i := range s.Connections {
		c := &s.Connections[i]
		if c.A.Type == model.NodeAsset && c.B.Type == model.NodeInternet {
			c.AToB = append(c.AToB, rules...)
			return
		}
	}
	t.Fatal("fixture moved: no asset-to-internet connection in the seed chart")
}

// Two ways of saying the same permission must produce one rule. AWS rejects the
// second with InvalidPermission.Duplicate, which fails an apply partway through —
// and `tofu validate` cannot see it, because each resource is valid alone and the
// two only collide at the API.
func TestIdenticalRulesAreMergedNotRepeated(t *testing.T) {
	s := model.Seed()
	internetEgress(t, &s,
		model.Rule{Protocol: "TCP", Port: "80", Detail: "package mirrors"},
		model.Rule{Protocol: "TCP", Port: "80", Detail: "container registry"})

	hcl, _ := generate(t, s)

	if n := strings.Count(hcl, `"asset_3_egress_tcp_80_80_`); n != 1 {
		t.Errorf("emitted %d rules for one permission, want 1:\n%s", n, hcl)
	}
	// Both reasons survive: naming one of two would misreport why the rule exists.
	for _, want := range []string{"package mirrors", "container registry"} {
		if !strings.Contains(hcl, want) {
			t.Errorf("merged rule lost the reason %q", want)
		}
	}
}

// Merging is on the permission, not on the pair of nodes. A different port is a
// different permission and must still be emitted.
func TestRulesDifferingOnlyByPortAreKept(t *testing.T) {
	s := model.Seed()
	internetEgress(t, &s,
		model.Rule{Protocol: "TCP", Port: "8080"},
		model.Rule{Protocol: "TCP", Port: "8081"})

	hcl, _ := generate(t, s)
	for _, want := range []string{"asset_3_egress_tcp_8080_8080_", "asset_3_egress_tcp_8081_8081_"} {
		if !strings.Contains(hcl, want) {
			t.Errorf("dropped a distinct permission: no %s in output", want)
		}
	}
}

// A gateway with nothing routed to it does nothing. Without this table both
// subnets are private, and an instance with a public IP and an open port 22 is
// still unreachable because the reply has no way out.
func TestSubnetsRouteToTheGateway(t *testing.T) {
	hcl, _ := generate(t, model.Seed())

	i := strings.Index(hcl, `resource "aws_route_table" "acc_1"`)
	if i < 0 {
		t.Fatalf("no route table for acc_1:\n%s", hcl)
	}
	table := hcl[i : i+strings.Index(hcl[i:], "\n}\n")]
	for _, want := range []string{
		`vpc_id = aws_vpc.acc_1.id`,
		`cidr_block = "0.0.0.0/0"`,
		`gateway_id = aws_internet_gateway.acc_1.id`,
	} {
		if !strings.Contains(collapse(table), collapse(want)) {
			t.Errorf("route table missing %q:\n%s", want, table)
		}
	}

	// Both subnets: an asset may land in either, and a subnet left unassociated
	// falls back to the main route table, which has no gateway route.
	for _, subnet := range []string{"acc_1", "acc_1_b"} {
		want := `resource "aws_route_table_association" "` + subnet + `"`
		if !strings.Contains(hcl, want) {
			t.Errorf("subnet %s is not associated with the route table", subnet)
		}
	}
}

// A firewall rule permits traffic; it does not deliver it. An asset reachable
// only in theory generates and applies cleanly, so the warning is the only thing
// that says so.
func TestInboundToAssetWithNoPublicAddressWarns(t *testing.T) {
	s := model.Seed()
	a := &s.Accounts[0].Assets[2]
	if a.Code != "EC2" {
		t.Fatalf("fixture moved: expected EC2, got %q", a.Code)
	}
	a.Params["associate_public_ip_address"] = false

	_, warnings := generate(t, s)
	got := joined(warnings)
	if !strings.Contains(got, "no public address") {
		t.Errorf("no warning for an unreachable target: %v", warnings)
	}
	// Naming the field label is the point — the user cannot act on an HCL argument
	// they never see in the drawer.
	if !strings.Contains(got, `turn on "Assign public IP"`) {
		t.Errorf("warning does not name the field to turn on: %v", warnings)
	}
}

// Turning the address on clears it, rather than the warning being unconditional
// on anything reachable from outside.
func TestInboundWithAPublicAddressDoesNotWarn(t *testing.T) {
	s := model.Seed()
	s.Accounts[0].Assets[2].Params["associate_public_ip_address"] = true

	_, warnings := generate(t, s)
	if strings.Contains(joined(warnings), "no public address") {
		t.Errorf("warned about an asset that has a public address: %v", warnings)
	}
}

// The false positive worth guarding: a same-account peer reaches the asset over
// private addressing through a security group reference, so it needs no public
// address at all. The seed chart is full of these — ALB to EC2, EC2 to RDS.
func TestSameAccountInboundNeverWarns(t *testing.T) {
	s := model.Seed()
	// Strip the chart back to internal traffic only, so anything left that warns
	// is a same-account flow.
	var internal []model.Connection
	for _, c := range s.Connections {
		if c.A.Type == model.NodeAsset && c.B.Type == model.NodeAsset {
			internal = append(internal, c)
		}
	}
	if len(internal) == 0 {
		t.Fatal("fixture moved: no asset-to-asset connections in the seed chart")
	}
	s.Connections = internal
	for i := range s.Accounts {
		for j := range s.Accounts[i].Assets {
			s.Accounts[i].Assets[j].Params["associate_public_ip_address"] = false
		}
	}

	_, warnings := generate(t, s)
	if strings.Contains(joined(warnings), "no public address") {
		t.Errorf("warned about private traffic between assets in one account: %v", warnings)
	}
}
