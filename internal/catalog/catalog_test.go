package catalog

import (
	"regexp"
	"testing"
)

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

// gate is the condition under which a field, Fixed or companion applies, so two
// writers of the same argument can be checked for whether they can ever collide.
type gate struct{ param, value string }

// exclusive reports whether two gates can never both be satisfied.
//
// Only one form is provable: the same directive required to equal two different
// values. Everything else is treated as a possible collision, because a false
// "these cannot both fire" is exactly the silent double-write these tests exist
// to catch.
func exclusive(a, b gate) bool {
	return a.param != "" && a.param == b.param &&
		a.value != "" && b.value != "" && a.value != b.value
}

// A Fixed value silently overriding an editable field would give the user a
// control that does nothing — the same class of bug as a security group that is
// created but never attached.
//
// Two writers of one argument are allowed only where their gates are mutually
// exclusive. The AMI presets are the case: each lookup sets ami, and so does the
// custom text field, but a select cannot hold two values at once.
func TestFixedNeverCollidesWithAnEditableField(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			editable := map[string][]gate{}
			for _, f := range rt.Params {
				if !f.Directive {
					editable[f.ParamKey()] = append(editable[f.ParamKey()], gate{f.RequiresParam, f.RequiresValue})
				}
			}
			// A companion writes its ParentRef onto the parent, so a Fixed or a
			// field with the same key would set that argument twice — which
			// OpenTofu rejects as "Attribute redefined".
			parentRefs := map[string][]gate{}
			for _, c := range rt.Companions {
				if c.ParentRef != "" {
					parentRefs[c.ParentRef] = append(parentRefs[c.ParentRef], gate{c.RequiresParam, c.RequiresValue})
				}
			}
			collides := func(what, key string, g gate, others []gate) {
				for _, o := range others {
					if !exclusive(g, o) {
						t.Errorf("%s/%s: %q is set by both %s and %+v, which can both apply",
							p.Key, rt.Code, key, what, o)
					}
				}
			}
			for _, fx := range rt.Fixed {
				key := fx.Key
				if fx.Block != "" {
					key = fx.Block + "." + fx.Key
				}
				g := gate{fx.RequiresParam, fx.RequiresValue}
				collides("a Fixed value and an editable field", key, g, editable[key])
				if fx.Block == "" {
					collides("a Fixed value and a companion ParentRef", key, g, parentRefs[fx.Key])
				}
			}
			for key, gates := range parentRefs {
				for _, g := range gates {
					collides("a companion ParentRef and an editable field", key, g, editable[key])
				}
			}
		}
	}
}

// Two companions may share a suffix only when they can never both be emitted —
// otherwise they would produce the same resource address twice. The AMI presets
// share one deliberately, so switching operating system edits a data source
// rather than moving it to a new address.
func TestCompanionsSharingASuffixAreExclusive(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			for i, a := range rt.Companions {
				for _, b := range rt.Companions[i+1:] {
					if a.Suffix != b.Suffix {
						continue
					}
					if !exclusive(gate{a.RequiresParam, a.RequiresValue}, gate{b.RequiresParam, b.RequiresValue}) {
						t.Errorf("%s/%s: companions %q and %q share suffix %q and can both apply",
							p.Key, rt.Code, a.TofuType, b.TofuType, a.Suffix)
					}
					// Sharing a suffix across two types changes the resource address,
					// which for a resource means destroy-and-recreate. A data source
					// holds no state and is only ever read, so there is nothing to
					// move — which is what lets one OS resolve through a published
					// parameter and another through a name match.
					if a.TofuType != b.TofuType && !(a.Data && b.Data) {
						t.Errorf("%s/%s: suffix %q is shared by resources of different types %q and %q, so switching between them would move state",
							p.Key, rt.Code, a.Suffix, a.TofuType, b.TofuType)
					}
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

// Companions are named from the asset ID plus a suffix, so a companion with
// neither has no address at all. Whether two may share a suffix is
// TestCompanionsSharingASuffixAreExclusive.
func TestCompanionsAreNamed(t *testing.T) {
	for _, p := range All() {
		for _, rt := range p.Types {
			for _, c := range rt.Companions {
				if c.TofuType == "" || c.Suffix == "" {
					t.Errorf("%s/%s: companion missing TofuType or Suffix: %+v", p.Key, rt.Code, c)
				}
			}
		}
	}
}

// A region reaches HCL verbatim as the provider's region, so an option has to be
// a region code and nothing else. A friendlier label such as "eu-west-2 (London)"
// would be stored and emitted as written, and fail at plan.
func TestRegionOptionsAreCodes(t *testing.T) {
	// Not a shape check — the four clouds disagree on that: eu-west-2, us-central1,
	// uksouth, lon1. What matters is that no label characters appear, since a space
	// or a bracket means a friendly name has leaked into a value.
	code := regexp.MustCompile(`^[a-z0-9-]+$`)
	for _, p := range All() {
		for _, f := range p.AccountParams {
			if f.Key != ParamRegion {
				continue
			}
			if len(f.Options) == 0 {
				t.Errorf("%s: region field with no options", p.Key)
			}
			seen := map[string]bool{}
			for _, opt := range f.Options {
				if !code.MatchString(opt) {
					t.Errorf("%s: %q is not a region code", p.Key, opt)
				}
				if seen[opt] {
					t.Errorf("%s: duplicate region %q", p.Key, opt)
				}
				seen[opt] = true
			}
			// The default has to be one of the offered values, or coerce silently
			// replaces it and the picker disagrees with what generation emits.
			if d, _ := f.Default.(string); !seen[d] {
				t.Errorf("%s: default region %q is not in the list", p.Key, d)
			}
		}
	}
}
