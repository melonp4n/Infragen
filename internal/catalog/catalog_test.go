package catalog

import "testing"

// The generator switches on Network and AddressKind to decide whether a
// connection becomes a firewall rule. A type that is missing or inconsistent in
// those fields produces silently wrong output, so the invariants are checked here
// rather than discovered in generated HCL.
func TestEveryResourceTypeIsClassified(t *testing.T) {
	networks := map[string]bool{NetFirewalled: true, NetServiceEndpoint: true, NetEdge: true}
	addrKinds := map[string]bool{AddrNone: true, AddrStaticIP: true, AddrEphemeralIP: true, AddrHostname: true}

	for _, p := range All() {
		seen := map[string]bool{}
		for _, rt := range p.Types {
			name := p.Key + "/" + rt.Code

			if seen[rt.Code] {
				t.Errorf("%s: duplicate code within provider", name)
			}
			seen[rt.Code] = true

			if rt.TofuType == "" {
				t.Errorf("%s: no TofuType", name)
			}
			if !networks[rt.Network] {
				t.Errorf("%s: Network is %q, want one of firewalled/service/edge", name, rt.Network)
			}
			if !addrKinds[rt.AddressKind] {
				t.Errorf("%s: AddressKind is %q", name, rt.AddressKind)
			}

			// An address kind without an attribute to read it from, or the
			// reverse, means one of the two was forgotten.
			if (rt.AddressKind == AddrNone) != (rt.AddressAttr == "") {
				t.Errorf("%s: AddressKind %q and AddressAttr %q disagree", name, rt.AddressKind, rt.AddressAttr)
			}

			// Service endpoints have no firewall, so an address on one would
			// invite a rule that cannot exist.
			if rt.Network == NetServiceEndpoint && rt.AddressKind != AddrNone {
				t.Errorf("%s: service endpoint should expose no address, has %q", name, rt.AddressKind)
			}

			// Only a firewalled resource can be given a durable address to attach
			// rules against.
			if rt.StaticAddr != nil && rt.Network != NetFirewalled {
				t.Errorf("%s: StaticAddr set on a %s resource", name, rt.Network)
			}

			// Without a StaticAddr, an ephemeral address is a dead end: the
			// generator refuses the rule and the user has no way to fix it.
			if rt.AddressKind == AddrEphemeralIP && rt.StaticAddr == nil {
				t.Errorf("%s: ephemeral address with no StaticAddr to offer the user", name)
			}
			if rt.StaticAddr != nil && (rt.StaticAddr.TofuType == "" || rt.StaticAddr.Attr == "") {
				t.Errorf("%s: incomplete StaticAddr %+v", name, *rt.StaticAddr)
			}

			// The StaticAddr and the toggle are two halves of one mechanism: the
			// toggle with no StaticAddr does nothing, and the StaticAddr with no
			// toggle can never be switched on.
			if hasToggle(rt) != (rt.StaticAddr != nil) {
				t.Errorf("%s: StaticAddr is %v but %s toggle present is %v",
					name, rt.StaticAddr != nil, ParamStaticPublicIP, hasToggle(rt))
			}
		}
	}
}

// Defaults seeds a new asset's params, so every declared field must appear.
func TestDefaultsCoverEveryParam(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			got := Defaults(p.Key, rt.Code)
			if len(got) != len(rt.Params) {
				t.Errorf("%s/%s: Defaults returned %d params, want %d", p.Key, rt.Code, len(got), len(rt.Params))
			}
			for _, f := range rt.Params {
				// Keyed by ParamKey, not Key: a leaf name reused in a block would
				// otherwise look present when it is not.
				if _, ok := got[f.ParamKey()]; !ok {
					t.Errorf("%s/%s: Defaults missing %q", p.Key, rt.Code, f.ParamKey())
				}
			}
		}
	}
}

func hasToggle(rt ResourceType) bool {
	for _, f := range rt.Params {
		if f.Key == ParamStaticPublicIP {
			return true
		}
	}
	return false
}

// A directive steers generation and is not an argument on the resource, so it
// must never reach the emitted configuration.
func TestArgumentsExcludeDirectives(t *testing.T) {
	sawDirective := false
	for _, p := range All() {
		for _, rt := range p.Types {
			for _, f := range rt.Params {
				if f.Directive {
					sawDirective = true
				}
			}
			for _, f := range rt.Arguments() {
				if f.Directive {
					t.Errorf("%s/%s: directive %q returned as an HCL argument", p.Key, rt.Code, f.Key)
				}
			}
		}
	}
	if !sawDirective {
		t.Error("no directive params found, so this test proved nothing")
	}
}

// The toggle is a real param, so it must still be seeded into a new asset —
// otherwise the drawer renders it unchecked while the session carries no value.
func TestStaticToggleIsSeeded(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			if rt.StaticAddr == nil {
				continue
			}
			if _, ok := Defaults(p.Key, rt.Code)[ParamStaticPublicIP]; !ok {
				t.Errorf("%s/%s: %s missing from Defaults", p.Key, rt.Code, ParamStaticPublicIP)
			}
		}
	}
}

// A Fixed value silently overriding an editable field would give the user a
// control that does nothing — the same class of bug as a security group that is
// created but never attached.
func TestFixedNeverCollidesWithAnEditableField(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			editable := map[string]bool{}
			for _, f := range rt.Params {
				if !f.Directive {
					editable[f.ParamKey()] = true
				}
			}
			// A companion writes its ParentRef onto the parent, so a Fixed or a
			// field with the same key would set that argument twice — which
			// OpenTofu rejects as "Attribute redefined".
			parentRefs := map[string]bool{}
			for _, c := range rt.Companions {
				if c.ParentRef != "" {
					parentRefs[c.ParentRef] = true
				}
			}
			for _, fx := range rt.Fixed {
				key := fx.Key
				if fx.Block != "" {
					key = fx.Block + "." + fx.Key
				}
				if editable[key] {
					t.Errorf("%s/%s: %q is both a Fixed value and an editable field",
						p.Key, rt.Code, key)
				}
				if fx.Block == "" && parentRefs[fx.Key] {
					t.Errorf("%s/%s: %q is set by both a Fixed value and a companion ParentRef",
						p.Key, rt.Code, fx.Key)
				}
			}
			for key := range parentRefs {
				if editable[key] {
					t.Errorf("%s/%s: %q is set by both a companion ParentRef and an editable field",
						p.Key, rt.Code, key)
				}
			}
		}
	}
}

// Params are keyed by block path, so two fields may share a leaf name only if
// they sit in different blocks. A genuine duplicate would have one silently
// overwrite the other in Asset.Params.
func TestParamKeysAreUniquePerType(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			seen := map[string]bool{}
			for _, f := range rt.Params {
				if seen[f.ParamKey()] {
					t.Errorf("%s/%s: duplicate param key %q", p.Key, rt.Code, f.ParamKey())
				}
				seen[f.ParamKey()] = true
			}
		}
	}
}

func TestParamKeyIncludesBlock(t *testing.T) {
	flat := ParamField{Key: "tier"}
	nested := ParamField{Key: "tier", Block: "settings"}
	deep := ParamField{Key: "size", Block: "boot_disk.initialize_params"}

	if flat.ParamKey() != "tier" {
		t.Errorf("top-level key = %q", flat.ParamKey())
	}
	if nested.ParamKey() != "settings.tier" {
		t.Errorf("nested key = %q", nested.ParamKey())
	}
	if deep.ParamKey() != "boot_disk.initialize_params.size" {
		t.Errorf("deep key = %q", deep.ParamKey())
	}
	if flat.ParamKey() == nested.ParamKey() {
		t.Error("a leaf name reused in a block collides with the top-level one")
	}
}

// Companions are named from the asset ID plus a suffix, so a suffix is what makes
// two companions on one type distinguishable.
func TestCompanionSuffixesAreUnique(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			seen := map[string]bool{}
			for _, c := range rt.Companions {
				if c.TofuType == "" || c.Suffix == "" {
					t.Errorf("%s/%s: companion missing TofuType or Suffix: %+v", p.Key, rt.Code, c)
				}
				if seen[c.Suffix] {
					t.Errorf("%s/%s: duplicate companion suffix %q", p.Key, rt.Code, c.Suffix)
				}
				seen[c.Suffix] = true
			}
		}
	}
}
