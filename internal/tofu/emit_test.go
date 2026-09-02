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
