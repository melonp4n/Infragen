package tofu

import (
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

func TestExpandPlaceholders(t *testing.T) {
	got := expand("azurerm_resource_group.{{account}}.name / {{asset}} / {{region}}", exprCtx{accountID: "acc_1", assetID: "asset_9"})
	want := "azurerm_resource_group.acc_1.name / asset_9 / var.acc_1_region"
	if got != want {
		t.Errorf("expand = %q, want %q", got, want)
	}
}

// A catalog expression containing HCL's own interpolation must survive untouched,
// or a reference to another resource would be mangled.
func TestExpandLeavesHCLInterpolationAlone(t *testing.T) {
	in := "${aws_eip.{{asset}}.public_ip}/32"
	if got := expand(in, exprCtx{accountID: "acc_1", assetID: "asset_2"}); got != "${aws_eip.asset_2.public_ip}/32" {
		t.Errorf("expand = %q", got)
	}
}

// Arguments come before nested blocks regardless of declaration order, so a block
// never splits two arguments that should align together.
func TestBlockNodeOrdersArgumentsBeforeBlocks(t *testing.T) {
	n := &blockNode{}
	n.add("settings", "tier", `"db-f1-micro"`)
	n.add("", "name", `"thing"`)
	n.add("settings.ip_configuration", "ipv4_enabled", "false")
	n.add("", "region", `"europe-west2"`)

	w := &writer{}
	n.render(w)
	got := w.String()

	nameAt := strings.Index(got, "name")
	settingsAt := strings.Index(got, "settings {")
	if nameAt == -1 || settingsAt == -1 || nameAt > settingsAt {
		t.Errorf("arguments did not precede blocks:\n%s", got)
	}
	if !strings.Contains(got, "    ipv4_enabled = false") {
		t.Errorf("nested block not indented as a child:\n%s", got)
	}
}

// registerTestType installs a provider exercising blocks, fixed values,
// companions and variables, without touching the real catalog entries.
func registerTestType(t *testing.T) model.Session {
	t.Helper()
	// The registry is global, so this must not outlive the test.
	t.Cleanup(func() { catalog.Unregister("emitcloud") })
	catalog.Register(catalog.Provider{
		Key: "emitcloud", Label: "EmitCloud", Color: "#777777", Dim: "#222222",
		TofuLocalName: "emitcloud", TofuSource: "test/emitcloud",
		Types: []catalog.ResourceType{{
			Code: "ET", Name: "Emit thing", TofuType: "emitcloud_thing",
			Network: catalog.NetFirewalled,
			Params: []catalog.ParamField{
				{Key: "size", Label: "Size", Type: catalog.FieldText, Default: "small"},
				{Key: "image", Block: "boot_disk.initialize_params", Label: "Image",
					Type: catalog.FieldText, Default: "debian-12"},
			},
			Fixed: []catalog.Fixed{
				{Key: "resource_group", Expr: "emitcloud_group.{{account}}.name"},
				{Block: "identity", Key: "type", Expr: `"SystemAssigned"`},
			},
			Companions: []catalog.Companion{{
				TofuType: "emitcloud_nic", Suffix: "nic",
				Fixed:      []catalog.Fixed{{Key: "subnet", Expr: "emitcloud_subnet.{{account}}.id"}},
				ParentRef:  "network_interface_ids",
				ParentExpr: "[emitcloud_nic.{{asset}}_nic.id]",
			}},
			Variables: []catalog.Variable{{
				Name: "{{account}}_admin_key", Description: "SSH public key", Sensitive: true,
			}},
		}},
	})

	s := model.Session{
		Version: model.SchemaVersion,
		Accounts: []model.Account{{
			ID: "acc_e", Name: "Emit", Provider: "emitcloud",
			Assets: []model.Asset{{
				ID: "asset_e", Code: "ET", Name: "Thing",
				Params: catalog.Defaults("emitcloud", "ET"),
			}},
		}},
	}
	model.Normalise(&s)
	return s
}

func TestEmitsBlocksFixedCompanionsAndVariables(t *testing.T) {
	hcl, _ := Generate(registerTestType(t))
	// Arguments are padded for alignment, so assertions compare on collapsed
	// whitespace rather than guessing the padding.
	flat := collapse(hcl)

	for _, want := range []string{
		// A param with a Block path lands nested, not at the top level.
		"boot_disk {",
		"initialize_params {",
		`image = "debian-12"`,
		// Fixed values are expanded and placed by their own block path.
		"resource_group = emitcloud_group.acc_e.name",
		"identity {",
		`type = "SystemAssigned"`,
		// The companion is named from the asset ID, and the parent references it.
		`resource "emitcloud_nic" "asset_e_nic"`,
		"subnet = emitcloud_subnet.acc_e.id",
		"network_interface_ids = [emitcloud_nic.asset_e_nic.id]",
		// Sensitive variables carry no default.
		`variable "acc_e_admin_key"`,
		"sensitive = true",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q from:\n%s", want, hcl)
		}
	}

	// A secret with a default would defeat the point of prompting for it.
	varBlock := hcl[strings.Index(hcl, `variable "acc_e_admin_key"`):]
	if end := strings.Index(varBlock, "\n}"); end > 0 {
		if strings.Contains(varBlock[:end], "default") {
			t.Error("sensitive variable was given a default")
		}
	}
}

// The whole point of Companion is that adding one is a data edit. If a companion
// is emitted but nothing points at it, it is dead HCL.
func TestEveryCompanionIsReferenced(t *testing.T) {
	checked := 0
	for _, p := range catalog.All() {
		for _, rt := range p.Types {
			for _, c := range rt.Companions {
				checked++
				if c.ParentRef == "" && c.ParentExpr == "" {
					continue // deliberately standalone, e.g. a security config resource
				}
				if c.ParentRef == "" || c.ParentExpr == "" {
					t.Errorf("%s/%s companion %q: ParentRef and ParentExpr must be set together",
						p.Key, rt.Code, c.Suffix)
				}
				if !strings.Contains(c.ParentExpr, c.Suffix) {
					t.Errorf("%s/%s companion %q: ParentExpr %q does not name the companion",
						p.Key, rt.Code, c.Suffix, c.ParentExpr)
				}
			}
		}
	}
	if checked == 0 {
		t.Skip("no companions declared yet — provider rewrites have not started")
	}
}
