// Package catalog is the table of cloud providers, the resource types each one
// offers, and the parameters each resource type exposes in the edit drawer.
//
// It drives both the UI (type menus, param forms) and OpenTofu generation, so a
// new provider or resource type is added here and nowhere else.
package catalog

import "sort"

// Field types a ParamField can take. These map to form controls in the drawer.
const (
	FieldText    = "text"
	FieldNumber  = "number"
	FieldSelect  = "select"
	FieldBoolean = "boolean"
)

// ParamField is one editable attribute of a resource, rendered as a form row and
// emitted as an HCL argument.
//
// Key must be the argument's real name and Block its real position, checked
// against a provider schema — see documentation/INVALID-PARAMS.md for what
// happens when they are written from memory instead.
type ParamField struct {
	Key string `json:"key"`
	// Block is the dot path to the containing block, empty for a top-level
	// argument. For example "settings" or "boot_disk.initialize_params".
	Block   string   `json:"block,omitempty"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
	// Default is also the secure choice where the field has security meaning.
	// Nothing else marks such a field, and nothing checks whether it was changed.
	Default any `json:"default"`

	// Advanced hides the field behind the drawer's disclosure. Reserve the visible
	// set for what someone picks when creating the asset.
	Advanced bool `json:"advanced,omitempty"`

	// Directive marks a param that steers generation rather than naming an HCL
	// argument. ParamStaticPublicIP is one: aws_instance has no such argument, so
	// emitting it verbatim would produce invalid configuration.
	Directive bool `json:"directive,omitempty"`
}

// ParamKey is how this field is keyed in Asset.Params. Two fields can share a
// leaf name in different blocks, so the block path is part of the identity.
func (f ParamField) ParamKey() string {
	if f.Block == "" {
		return f.Key
	}
	return f.Block + "." + f.Key
}

// Fixed is a required argument with exactly one sensible answer and no user
// opinion: a CloudFront origin_id, a task definition family.
//
// Never a security setting. Those are ordinary editable fields whose Default is
// the secure value, so the user can change them.
type Fixed struct {
	Block string `json:"block,omitempty"`
	Key   string `json:"key"`
	// Expr is rendered HCL, not a Go value: it may reference other resources.
	Expr string `json:"expr"`
}

// Variable is a value the chart cannot supply and the user must, such as an SSH
// public key or a database administrator password.
//
// Sensitive variables are emitted with no default, so `tofu plan` prompts rather
// than a secret living in the configuration or the state file. Name may use the
// {{account}} and {{asset}} placeholders.
type Variable struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // defaults to string
	// Default is rendered HCL. Never set it on a sensitive variable: a default
	// means no prompt, so the secret would silently be whatever was defaulted.
	Default   string `json:"default,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// Companion is a resource emitted alongside an asset because the asset cannot
// exist without it — an IAM role for a Lambda, a network interface for an Azure
// VM. 13 of the 26 types need at least one; see documentation/TOFU-MAPPING.md.
//
// Companions exist so that adding one stays a data edit in the catalog rather
// than a branch in the emitter.
type Companion struct {
	TofuType string `json:"tofuType"`
	// Suffix is appended to the asset ID to name the companion, so it inherits
	// the guarantee that renaming an asset never moves a resource address.
	Suffix string  `json:"suffix"`
	Fixed  []Fixed `json:"fixed,omitempty"`
	// ParentRef is the argument on the asset that points at this companion, and
	// ParentExpr is what to put there. Empty when nothing needs to reference it.
	ParentRef  string `json:"parentRef,omitempty"`
	ParentExpr string `json:"parentExpr,omitempty"`
}

// ParamStaticPublicIP is the toggle that opts an asset into a durable address.
// The catalog and the generator both need the key, so it is named once here.
const ParamStaticPublicIP = "static_public_ip"

// StaticAddressToggle is the param to attach to any type carrying a StaticAddr.
func StaticAddressToggle() ParamField {
	return ParamField{
		Key: ParamStaticPublicIP, Label: "Static public IP", Type: FieldBoolean,
		Default: false, Directive: true,
	}
}

// How a resource is reached, and what governs access to it. This decides whether
// a connection becomes a firewall rule at all — see documentation/TOFU-MAPPING.md.
const (
	// NetFirewalled has an IP inside a VPC or VNet and a firewall to attach
	// rules to: instances, VMs, droplets, managed databases, load balancers.
	NetFirewalled = "firewalled"
	// NetServiceEndpoint is reached at a public service endpoint and governed by
	// IAM or a resource policy. There is no firewall, so a firewall rule for one
	// of these applies cleanly and controls nothing.
	NetServiceEndpoint = "service"
	// NetEdge is a CDN. It has no stable egress IP, so origin access is
	// controlled by configuration and header auth rather than by address.
	NetEdge = "edge"
)

// What kind of address a resource exposes. Three of these are genuinely
// different for rule generation, which is why this is not a "stable" boolean.
const (
	AddrNone        = ""             // no address attribute at all
	AddrStaticIP    = "static-ip"    // an IP that does not change
	AddrEphemeralIP = "ephemeral-ip" // an IP that changes on restart
	AddrHostname    = "hostname"     // a DNS name; security groups cannot use it
)

// StaticAddress names the resource that gives an asset a durable address. It is
// emitted only when the user enables the static address param on the asset.
type StaticAddress struct {
	TofuType string `json:"tofuType"`
	Attr     string `json:"attr"`
}

// ResourceType is one deployable thing, e.g. an EC2 instance.
//
// Code is the short badge shown on the tile and the key stored in Asset.Code —
// it must be unique within a provider and stable, because saved sessions
// reference it.
type ResourceType struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	TofuType string `json:"tofuType"`

	// OmitName suppresses the provider's NameArg for this type. A few resources
	// have no name argument at all — a DigitalOcean app names itself inside its
	// spec block, a CDN endpoint has no name — and setting one is an error.
	OmitName bool `json:"omitName,omitempty"`

	// Fixed carries required arguments the user has no say in. Companions are
	// resources emitted alongside this one because it cannot exist without them.
	Fixed      []Fixed     `json:"fixed,omitempty"`
	Companions []Companion `json:"companions,omitempty"`
	Variables  []Variable  `json:"variables,omitempty"`

	Network     string         `json:"network"`
	AddressAttr string         `json:"addressAttr,omitempty"`
	AddressKind string         `json:"addressKind,omitempty"`
	StaticAddr  *StaticAddress `json:"staticAddr,omitempty"`

	Params []ParamField `json:"params"`
}

// Provider is one cloud. Colour and Dim carry the provider's identity through
// the UI: account dot, provider tag, asset badges.
type Provider struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Color string `json:"color"`
	Dim   string `json:"dim"`

	// TofuLocalName is the provider's name in HCL, which is not always the key —
	// "azure" is spelled "azurerm". TofuSource is its registry address.
	TofuLocalName string `json:"tofuLocalName"`
	TofuSource    string `json:"tofuSource"`

	// NameArg is the argument that carries a resource's generated name, and
	// LabelArg the one that carries its display label. The clouds disagree: AWS
	// and Azure take tags, GCP and DigitalOcean take a name. Empty means the
	// provider does not use one.
	NameArg  string `json:"nameArg,omitempty"`
	LabelArg string `json:"labelArg,omitempty"`

	// Config is what goes inside the provider block. The clouds disagree: AWS
	// takes a region, azurerm requires an empty features block, Google needs a
	// project. A Fixed with an empty Key declares a block and nothing else.
	Config []Fixed `json:"config,omitempty"`
	// Variables the provider block itself needs, such as a GCP project ID.
	Variables []Variable `json:"variables,omitempty"`

	Types []ResourceType `json:"types"`
}

var registry = map[string]Provider{}

// Register adds a provider to the catalog. Each provider file calls this from
// its init(), so importing the package is enough to make the provider available.
func Register(p Provider) {
	registry[p.Key] = p
}

// Unregister removes a provider. Tests that register a synthetic provider must
// call this on cleanup: the registry is global, so a leftover entry reaches
// anything that walks All() — including generated configuration, which then
// demands a provider that does not exist.
func Unregister(key string) {
	delete(registry, key)
}

// Get returns a provider by key.
func Get(key string) (Provider, bool) {
	p, ok := registry[key]
	return p, ok
}

// All returns every registered provider, ordered by key so the UI and generated
// output are deterministic.
func All() []Provider {
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Type looks up one resource type within a provider.
func Type(provider, code string) (ResourceType, bool) {
	p, ok := registry[provider]
	if !ok {
		return ResourceType{}, false
	}
	for _, t := range p.Types {
		if t.Code == code {
			return t, true
		}
	}
	return ResourceType{}, false
}

// Defaults builds the initial Params map for a newly added asset.
func Defaults(provider, code string) map[string]any {
	out := map[string]any{}
	t, ok := Type(provider, code)
	if !ok {
		return out
	}
	for _, f := range t.Params {
		out[f.ParamKey()] = f.Default
	}
	return out
}

// Arguments returns the params that map to HCL arguments, skipping generator
// directives. Emitters must use this rather than ranging over Params, or a
// directive ends up in the output as an argument that does not exist.
func (r ResourceType) Arguments() []ParamField {
	out := make([]ParamField, 0, len(r.Params))
	for _, f := range r.Params {
		if !f.Directive {
			out = append(out, f)
		}
	}
	return out
}
