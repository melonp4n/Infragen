package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// ansibleSeed enables Ansible on the web tier, which the seed chart already gives
// SSH ingress from the jump host and a static-address toggle.
func ansibleSeed(t *testing.T, tweak func(*model.Asset)) (model.Session, string, []string) {
	t.Helper()
	s := model.Seed()
	a := &s.Accounts[0].Assets[2] // Web tier EC2
	if a.Code != "EC2" {
		t.Fatalf("fixture moved: expected EC2, got %q", a.Code)
	}
	a.Params[catalog.ParamAnsible] = true
	a.Params[catalog.ParamAnsibleGroup] = "web"
	a.Params[catalog.ParamStaticPublicIP] = true
	if tweak != nil {
		tweak(a)
	}
	hcl, warnings := generate(t, s)
	return s, hcl, warnings
}

func joined(w []string) string { return strings.Join(w, "\n") }

// Four sources, first non-empty wins. Only the ones actually set appear, so a host
// with no per-resource key resolves through the two variables alone.
func TestSSHKeyResolutionOrder(t *testing.T) {
	cases := []struct {
		name  string
		tweak func(*model.Asset)
		want  string
	}{
		{"variables only", nil,
			`coalesce(var.acc_1_ssh_public_key, var.ssh_public_key, "")`},
		{"pasted key first", func(a *model.Asset) {
			a.Params[catalog.ParamSSHPublicKey] = "ssh-ed25519 AAAAC3Nz"
		}, `coalesce("ssh-ed25519 AAAAC3Nz", var.acc_1_ssh_public_key, var.ssh_public_key, "")`},
		{"file becomes a file() call", func(a *model.Asset) {
			a.Params[catalog.ParamSSHPublicKeyFile] = "/keys/id.pub"
		}, `coalesce(file("/keys/id.pub"), var.acc_1_ssh_public_key, var.ssh_public_key, "")`},
		{"pasted wins over file", func(a *model.Asset) {
			a.Params[catalog.ParamSSHPublicKey] = "ssh-ed25519 AAAAC3Nz"
			a.Params[catalog.ParamSSHPublicKeyFile] = "/keys/id.pub"
		}, `coalesce("ssh-ed25519 AAAAC3Nz", file("/keys/id.pub"), var.acc_1_ssh_public_key, var.ssh_public_key, "")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, hcl, _ := ansibleSeed(t, c.tweak)
			// Resolution lives in a local so count and the value share it.
			if !strings.Contains(collapse(hcl), "asset_3_ssh_key = "+c.want) {
				t.Errorf("key resolution wrong, wanted %s", c.want)
			}
			if !strings.Contains(collapse(hcl), "public_key = local.asset_3_ssh_key") {
				t.Error("key pair does not read the resolved local")
			}
		})
	}
}

// SSH access is not the same want as Ansible. A machine gets key wiring whether or
// not anything configures it, because people SSH into hosts.
func TestKeyWiringIsIndependentOfAnsible(t *testing.T) {
	hcl, _ := generate(t, model.Seed()) // no Ansible anywhere
	flat := collapse(hcl)

	for _, want := range []string{
		`resource "aws_key_pair" "asset_3_key"`,
		`resource "digitalocean_ssh_key" "asset_7_key"`,
		"asset_3_ssh_key = coalesce(",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q — SSH access should not require Ansible", want)
		}
	}
	// Nothing is created unless a key actually resolves, and that decision has to
	// be made at plan time because a key may come from a variable.
	if !strings.Contains(flat, `count = local.asset_3_ssh_key != "" ? 1 : 0`) {
		t.Error("key pair is not conditional on a key existing")
	}
	// A conditional resource cannot be referenced directly.
	if !strings.Contains(flat, "key_name = one(aws_key_pair.asset_3_key[*].key_name)") {
		t.Error("instance does not reference the key pair through one()")
	}
	// Ansible still adds nothing on its own here.
	if strings.Contains(hcl, "precondition") {
		t.Error("emitted a key precondition on a chart with no Ansible hosts")
	}
}

// A chart of nothing but service endpoints has no machine to log into, so it must
// not ask for a key.
func TestNoKeyWiringWithoutMachines(t *testing.T) {
	s := model.Session{
		Version: model.SchemaVersion,
		Accounts: []model.Account{{
			ID: "acc_1", Name: "Storage only", Provider: "aws",
			Assets: []model.Asset{{
				ID: "asset_1", Code: "S3", Name: "Bucket",
				Params: catalog.Defaults("aws", "S3"),
			}},
		}},
	}
	hcl, _ := generate(t, s)
	for _, bad := range []string{"ssh_public_key", "aws_key_pair", "_ssh_key"} {
		if strings.Contains(hcl, bad) {
			t.Errorf("emitted %q for a chart with no machines", bad)
		}
	}
}

// The precondition is what catches a host with no key, because infrachart cannot
// see whether the variable is set. It must name the asset and the fix.
func TestKeyPreconditionNamesTheAssetAndTheFix(t *testing.T) {
	_, hcl, _ := ansibleSeed(t, nil)
	flat := collapse(hcl)

	if !strings.Contains(flat, `condition = var.ssh_public_key != "" || var.acc_1_ssh_public_key != ""`) {
		t.Error("no precondition guarding the key")
	}
	for _, want := range []string{`Web tier EC2`, "ssh-keygen -t ed25519", "TF_VAR_ssh_public_key"} {
		if !strings.Contains(hcl, want) {
			t.Errorf("precondition message missing %q", want)
		}
	}
	// Quotes in the message must be escaped, or the error_message breaks the file.
	if strings.Contains(hcl, `on "Web tier EC2" but`) {
		t.Error("asset name is unescaped inside the error_message string")
	}
}

// A per-resource key makes the precondition always true, so emitting it would be
// noise on every plan.
func TestNoPreconditionWhenAKeyIsSetOnTheResource(t *testing.T) {
	_, hcl, _ := ansibleSeed(t, func(a *model.Asset) {
		a.Params[catalog.ParamSSHPublicKey] = "ssh-ed25519 AAAAC3Nz"
	})
	if strings.Contains(hcl, "precondition") {
		t.Error("emitted a precondition that can never fail")
	}
}

// The warning that prompted the feature: Ansible needs SSH, and a chart can
// easily declare a host with none.
func TestWarnsWhenAnsibleHostHasNoSSHIngress(t *testing.T) {
	// The Azure VM has no inbound rules at all in the seed chart.
	s := model.Seed()
	vm := &s.Accounts[3].Assets[0]
	vm.Params[catalog.ParamAnsible] = true
	vm.Params[catalog.ParamAnsibleGroup] = "workers"
	_, warnings := generate(t, s)

	if !strings.Contains(joined(warnings), "nothing may reach it on port 22") {
		t.Errorf("no SSH ingress warning: %v", warnings)
	}
}

// An all-ports rule grants SSH. A naive equality check against 22 would miss it
// and warn about a host that is in fact reachable.
func TestAllPortsRuleCountsAsSSHIngress(t *testing.T) {
	s := model.Seed()
	a := &s.Accounts[0].Assets[2]
	a.Params[catalog.ParamAnsible] = true
	a.Params[catalog.ParamAnsibleGroup] = "web"
	a.Params[catalog.ParamStaticPublicIP] = true
	// Replace the jump host's port 22 rule with an all-ports one.
	for i := range s.Connections {
		if s.Connections[i].ID == "conn_7" {
			s.Connections[i].AToB[0].Port = "*"
		}
	}
	_, warnings := generate(t, s)

	if strings.Contains(joined(warnings), "nothing may reach it on port 22") {
		t.Errorf("an all-ports rule was not recognised as SSH ingress: %v", warnings)
	}
}

// A refused rule generates nothing, so it grants no access and must not count.
func TestRefusedRuleIsNotSSHIngress(t *testing.T) {
	s := model.Seed()
	a := &s.Accounts[0].Assets[2]
	a.Params[catalog.ParamAnsible] = true
	a.Params[catalog.ParamAnsibleGroup] = "web"
	a.Params[catalog.ParamStaticPublicIP] = true
	// An unset port is refused at generation, so this rule opens nothing.
	for i := range s.Connections {
		if s.Connections[i].ID == "conn_7" {
			s.Connections[i].AToB[0].Port = ""
		}
	}
	_, warnings := generate(t, s)

	if !strings.Contains(joined(warnings), "nothing may reach it on port 22") {
		t.Errorf("a refused rule was treated as granting SSH: %v", warnings)
	}
}

func TestAnsibleGroupWarnings(t *testing.T) {
	cases := map[string]struct{ group, want string }{
		"no group": {"", "has no group, so it lands in [ungrouped]"},
		// A bracket survives Normalise — it is not a control character — so the
		// section-name check is what catches it.
		"bracket":     {"web]", "not a valid inventory section name"},
		"space":       {"web servers", "not a valid inventory section name"},
		"interpolate": {"web${var.x}", "not a valid inventory section name"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, warnings := ansibleSeed(t, func(a *model.Asset) {
				a.Params[catalog.ParamAnsibleGroup] = c.group
			})
			if !strings.Contains(joined(warnings), c.want) {
				t.Errorf("missing %q in %v", c.want, warnings)
			}
		})
	}
}

// The group name reaches a heredoc where quote() does not apply, so it is the
// injection surface. Two independent layers stop it, and this asserts both.
func TestGroupNameCannotEscapeTheInventory(t *testing.T) {
	// Layer one: Normalise strips control characters from a text param, so a
	// newline never reaches the group check at all.
	s := model.Seed()
	a := &s.Accounts[0].Assets[2]
	a.Params[catalog.ParamAnsible] = true
	a.Params[catalog.ParamAnsibleGroup] = "web]\n[root"
	model.Normalise(&s)
	if got := a.Params[catalog.ParamAnsibleGroup]; got != "" {
		t.Errorf("a newline survived Normalise: %q", got)
	}

	// Layer two: anything that does survive must still match a section name.
	for _, bad := range []string{"web]", "[root", "a b", "x${y}", strings.Repeat("z", 65), ""} {
		asset := model.Asset{Params: map[string]any{catalog.ParamAnsibleGroup: bad}}
		if g, ok := group(&asset); ok {
			t.Errorf("group %q was accepted as %q", bad, g)
		} else if g != ungrouped {
			t.Errorf("rejected group %q fell back to %q, not %q", bad, g, ungrouped)
		}
	}

	// And a reasonable name still works, or the check is simply too strict.
	for _, good := range []string{"web", "db-primary", "app_servers", "eu.west"} {
		asset := model.Asset{Params: map[string]any{catalog.ParamAnsibleGroup: good}}
		if g, ok := group(&asset); !ok || g != good {
			t.Errorf("group %q was rejected", good)
		}
	}
}

// Two keys on one host is ambiguous, so it asks rather than deciding quietly.
func TestWarnsWhenBothKeySourcesAreSet(t *testing.T) {
	_, _, warnings := ansibleSeed(t, func(a *model.Asset) {
		a.Params[catalog.ParamSSHPublicKey] = "ssh-ed25519 AAAAC3Nz"
		a.Params[catalog.ParamSSHPublicKeyFile] = "/keys/id.pub"
	})
	if !strings.Contains(joined(warnings), "both a pasted SSH key and a key file") {
		t.Errorf("no ambiguity warning: %v", warnings)
	}
}

// The inventory groups by name across accounts and providers, and is written by
// Terraform rather than by infrachart.
func TestInventoryGroupsAcrossProviders(t *testing.T) {
	s := model.Seed()
	// Two hosts in one group, from different clouds and different accounts.
	ec2 := &s.Accounts[0].Assets[2]
	ec2.Params[catalog.ParamAnsible] = true
	ec2.Params[catalog.ParamAnsibleGroup] = "web"
	ec2.Params[catalog.ParamStaticPublicIP] = true

	droplet := &s.Accounts[2].Assets[0]
	droplet.Params[catalog.ParamAnsible] = true
	droplet.Params[catalog.ParamAnsibleGroup] = "web"

	// A third in its own group.
	vm := &s.Accounts[3].Assets[0]
	vm.Params[catalog.ParamAnsible] = true
	vm.Params[catalog.ParamAnsibleGroup] = "workers"
	vm.Params[catalog.ParamStaticPublicIP] = true

	hcl, _ := generate(t, s)

	for _, want := range []string{
		`resource "local_file" "ansible_inventory"`,
		"[web]",
		"[workers]",
		"${aws_eip.asset_3.public_ip} ansible_user=ec2-user",
		"${digitalocean_droplet.asset_7.ipv4_address} ansible_user=root",
		"${azurerm_public_ip.asset_9.ip_address} ansible_user=azureuser",
		"ansible_ssh_private_key_file=~/.ssh/id_ed25519",
	} {
		if !strings.Contains(hcl, want) {
			t.Errorf("inventory missing %q", want)
		}
	}

	// One group, not one per account.
	if n := strings.Count(hcl, "[web]"); n != 1 {
		t.Errorf("[web] appears %d times, want 1 — a group spanning accounts must merge", n)
	}
	// The local provider comes from the feature, not from an account.
	if !strings.Contains(collapse(hcl), `local = { source = "hashicorp/local" }`) {
		t.Error("local provider not declared for the inventory")
	}
}

// A host with no group is still deployed, and lands somewhere findable.
func TestUngroupedAnsibleHost(t *testing.T) {
	_, hcl, _ := ansibleSeed(t, func(a *model.Asset) {
		a.Params[catalog.ParamAnsibleGroup] = ""
	})
	if !strings.Contains(hcl, "[ungrouped]") {
		t.Error("a host with no group did not land in [ungrouped]")
	}
}

// An ephemeral address is fine in an inventory, and this is the case that made the
// feature look broken: an AWS-only chart produced no inventory at all, because the
// firewall rule's address test was wrongly reused here.
//
// The inventory is regenerated on every apply, so it always holds the current
// address. A firewall rule is not, which is why that one refuses.
func TestEphemeralAddressIsUsableInInventory(t *testing.T) {
	_, hcl, warnings := ansibleSeed(t, func(a *model.Asset) {
		a.Params[catalog.ParamStaticPublicIP] = false
		// The instance still has to have a public address for there to be an
		// ephemeral one to test — see TestHostWithNoPublicAddressIsRefused.
		a.Params["associate_public_ip_address"] = true
	})
	if !strings.Contains(hcl, "${aws_instance.asset_3.public_ip} ansible_user=ec2-user") {
		t.Errorf("host with an ephemeral address was dropped from the inventory:\n%s", hcl)
	}
	if strings.Contains(joined(warnings), "changes when the instance restarts") {
		t.Errorf("warned about an address that is fine for an inventory: %v", warnings)
	}
}

// An address attribute that is never populated is not an address. aws_instance's
// public_ip is empty on an instance with no public IP, so an unguarded reference
// wrote an inventory line with nothing in the address column — and an apply that
// looked like it had worked.
func TestHostWithNoPublicAddressIsRefused(t *testing.T) {
	_, hcl, warnings := ansibleSeed(t, func(a *model.Asset) {
		a.Params[catalog.ParamStaticPublicIP] = false
		a.Params["associate_public_ip_address"] = false
	})
	if strings.Contains(hcl, "aws_instance.asset_3.public_ip} ansible_user") {
		t.Errorf("emitted an inventory line for a host with no public address:\n%s", hcl)
	}
	// The warning has to name what the user clicks, not the HCL argument.
	if !strings.Contains(joined(warnings), `turn on "Assign public IP"`) {
		t.Errorf("no warning naming the field to turn on: %v", warnings)
	}
}

// A durable address is preferred when there is one, so the inventory does not
// change between applies.
func TestStaticAddressPreferredInInventory(t *testing.T) {
	_, hcl, _ := ansibleSeed(t, nil) // the helper enables the static toggle
	if !strings.Contains(hcl, "${aws_eip.asset_3.public_ip}") {
		t.Error("static address not preferred over the instance's own")
	}
}

// The only genuinely unusable case: a type with no address attribute at all.
// Unreachable today, since Ansible is limited to types that all have one — kept
// as a guard for whatever gets added next.
func TestHostWithNoAddressIsRefused(t *testing.T) {
	acc := model.Account{ID: "acc_1", Provider: "aws"}
	// ECS has Network firewalled but no address attribute.
	a := model.Asset{ID: "asset_x", Code: "ECS", Name: "Service"}
	if _, err := hostAddress(acc, &a); err == nil {
		t.Error("a type with no address was accepted into the inventory")
	}
}

// No Ansible means no inventory, no .gitignore, and no local provider to install.
func TestNoInventoryWithoutAnsible(t *testing.T) {
	hcl, _ := generate(t, model.Seed())
	for _, bad := range []string{
		`resource "local_file"`,
		"hashicorp/local",
		"inventory.ini",
	} {
		if strings.Contains(hcl, bad) {
			t.Errorf("emitted %q for a chart with no Ansible hosts", bad)
		}
	}
}

// An account's own settings reach generation: the region becomes the variable's
// default, and the account key is what a host with none of its own resolves to.
//
// Both were previously Terraform variables with nothing in the chart behind them,
// so a region could only be set with TF_VAR and never saved with the session.
func TestAccountSettingsReachGeneration(t *testing.T) {
	s := model.Seed()
	acc := &s.Accounts[0]
	a := &acc.Assets[2] // Web tier EC2
	a.Params[catalog.ParamAnsible] = true
	a.Params[catalog.ParamAnsibleGroup] = "web"
	a.Params["associate_public_ip_address"] = true
	acc.Params[catalog.ParamRegion] = "us-east-1"
	acc.Params[catalog.ParamSSHPublicKey] = "ssh-ed25519 AAAAaccountkey"

	hcl, warnings := generate(t, s)

	if !strings.Contains(collapse(hcl), `variable "`+acc.ID+`_region" { description = "Region for `+acc.Name+`" type = string default = "us-east-1"`) {
		t.Errorf("account region did not reach the region variable:\n%s", hcl)
	}
	// Ahead of both variables, because a chart value is knowable here.
	if !strings.Contains(hcl, `"ssh-ed25519 AAAAaccountkey", var.`+acc.ID+`_ssh_public_key`) {
		t.Errorf("account key is not a source for a host with no key of its own:\n%s", hcl)
	}
	// The precondition exists to catch a host with no key from anywhere. The
	// account supplies one, so it would only ever fail on a false alarm.
	if strings.Contains(hcl, "no SSH public key is set") {
		t.Error("kept the no-key precondition on a host whose account supplies a key")
	}
	if j := joined(warnings); strings.Contains(j, "SSH") {
		t.Errorf("unexpected SSH warning: %v", warnings)
	}
}
