package model

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"infrachart/internal/catalog"
)

// Session JSON arrives from the browser and from user-supplied files, so it is
// untrusted. Validate rejects anything that does not describe a coherent chart,
// and Normalise coerces asset params to the types the catalog declares — which
// is what keeps generated HCL free of arbitrary user-shaped values.

const (
	maxNameLen = 200
	// A startup script is a file, not a name, so it gets a far larger cap. Cloud
	// providers reject user data beyond about 16KB anyway.
	maxScriptLen = 16 << 10
	maxAccounts  = 200
	maxAssets    = 500
	maxRules     = 200
)

var (
	// A port, or an inclusive range. Empty means "any", matching the prototype.
	portPattern = regexp.MustCompile(`^(\d{1,5})(-(\d{1,5}))?$`)
	protocols   = map[string]bool{"TCP": true, "UDP": true, "ICMP": true, "ALL": true}
)

// Validate checks a session for structural and referential integrity. It returns
// the first problem found; callers should treat any error as a 400.
func Validate(s *Session) error {
	if s.Version != 0 && s.Version != SchemaVersion {
		return fmt.Errorf("unsupported session version %d (want %d)", s.Version, SchemaVersion)
	}
	if len(s.Accounts) > maxAccounts {
		return fmt.Errorf("too many accounts: %d", len(s.Accounts))
	}

	seen := map[string]bool{}
	claim := func(kind, id string) error {
		if id == "" {
			return fmt.Errorf("%s has an empty id", kind)
		}
		if seen[id] {
			return fmt.Errorf("duplicate id %q", id)
		}
		seen[id] = true
		return nil
	}

	for i := range s.Accounts {
		acc := &s.Accounts[i]
		if err := claim("account", acc.ID); err != nil {
			return err
		}
		if err := checkName("account name", acc.Name); err != nil {
			return err
		}
		if _, ok := catalog.Get(acc.Provider); !ok {
			return fmt.Errorf("unknown provider %q on account %q", acc.Provider, acc.ID)
		}
		if len(acc.Assets) > maxAssets {
			return fmt.Errorf("too many assets in account %q", acc.ID)
		}
		for j := range acc.Assets {
			as := &acc.Assets[j]
			if err := claim("asset", as.ID); err != nil {
				return err
			}
			if err := checkName("asset name", as.Name); err != nil {
				return err
			}
			if _, ok := catalog.Type(acc.Provider, as.Code); !ok {
				return fmt.Errorf("unknown resource type %q for provider %q", as.Code, acc.Provider)
			}
		}
	}

	for i := range s.ExternalIPs {
		e := &s.ExternalIPs[i]
		if err := claim("external ip", e.ID); err != nil {
			return err
		}
		if err := checkName("external ip label", e.Label); err != nil {
			return err
		}
		if _, err := ParseCIDR(e.IP); err != nil {
			return fmt.Errorf("external ip %q: %w", e.Label, err)
		}
	}

	for i := range s.Connections {
		c := &s.Connections[i]
		if err := claim("connection", c.ID); err != nil {
			return err
		}
		if err := s.checkNode(c.A); err != nil {
			return fmt.Errorf("connection %q endpoint A: %w", c.ID, err)
		}
		if err := s.checkNode(c.B); err != nil {
			return fmt.Errorf("connection %q endpoint B: %w", c.ID, err)
		}
		if c.A.Key() == c.B.Key() {
			return fmt.Errorf("connection %q joins a node to itself", c.ID)
		}
		if len(c.AToB)+len(c.BToA) > maxRules {
			return fmt.Errorf("too many rules on connection %q", c.ID)
		}
		for _, r := range append(append([]Rule{}, c.AToB...), c.BToA...) {
			if err := checkRule(r); err != nil {
				return fmt.Errorf("connection %q: %w", c.ID, err)
			}
		}
	}
	return nil
}

// checkNode confirms a NodeRef points at something that exists.
func (s *Session) checkNode(n NodeRef) error {
	switch n.Type {
	case NodeInternet:
		return nil
	case NodeExtIP:
		if s.ExternalIP(n.ID) == nil {
			return fmt.Errorf("no external ip %q", n.ID)
		}
		return nil
	case NodeAsset:
		acc := s.Account(n.AccountID)
		if acc == nil {
			return fmt.Errorf("no account %q", n.AccountID)
		}
		if acc.Asset(n.AssetID) == nil {
			return fmt.Errorf("no asset %q in account %q", n.AssetID, n.AccountID)
		}
		return nil
	default:
		return fmt.Errorf("unknown node type %q", n.Type)
	}
}

func checkRule(r Rule) error {
	if !protocols[r.Protocol] {
		return fmt.Errorf("unknown protocol %q", r.Protocol)
	}
	if err := checkName("rule note", r.Detail); err != nil {
		return err
	}
	_, _, _, err := parsePort(r.Port)
	return err
}

// parsePort is the single definition of what a port field may contain. Both
// validation and generation go through it: when they each had their own copy they
// disagreed, and a value one rejected the other silently read as "all ports".
//
// A blank field is unspecified, not "all ports". It is accepted here so a rule can
// be half-written without the drawer refusing to render, but it generates nothing
// — allowing every port has to be typed deliberately as "*".
func parsePort(port string) (lo, hi int, any bool, err error) {
	if strings.TrimSpace(port) == "" {
		return 0, 0, false, nil
	}
	if isAnyPort(port) {
		return 0, 0, true, nil
	}
	m := portPattern.FindStringSubmatch(port)
	if m == nil {
		return 0, 0, false, fmt.Errorf("malformed port %q — use a port, a range like 8000-8080, or %s for any", port, anyPortHint)
	}
	lo, _ = strconv.Atoi(m[1])
	hi = lo
	if m[3] != "" {
		hi, _ = strconv.Atoi(m[3])
	}
	if lo < 1 || hi > 65535 || lo > hi {
		return 0, 0, false, fmt.Errorf("port %q is outside 1-65535 — use %s for any", port, anyPortHint)
	}
	return lo, hi, false, nil
}

// anyPortHint is what the error messages tell people to type. Port 0 is not in
// the list: it is a real value in some protocols, so accepting it as a wildcard
// would make a typo mean "everything".
const anyPortHint = `"*"`

// AnyPortValue is how "all ports" is stored once normalised. It is deliberately
// something you have to type: a blank field must never mean allow-everything,
// because a rule someone abandoned half-written would then open every port.
const AnyPortValue = "*"

// isAnyPort reports whether a port field explicitly means all ports. Blank is not
// on this list — see AnyPortValue.
func isAnyPort(port string) bool {
	switch strings.ToLower(strings.TrimSpace(port)) {
	case "*", "any", "all":
		return true
	}
	return false
}

// PortUnset reports whether a rule has no port yet. Such a rule is not an error
// while editing, but it generates nothing.
func (r Rule) PortUnset() bool {
	return strings.TrimSpace(r.Port) == ""
}

func checkName(what, v string) error {
	if len(v) > maxNameLen {
		return fmt.Errorf("%s is too long (%d chars)", what, len(v))
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return fmt.Errorf("%s contains a control character", what)
	}
	return nil
}

// ParseCIDR accepts a bare address or a prefix and returns it in prefix form, so
// generated rules always carry an explicit mask. A bare address becomes /32 or
// /128 as appropriate.
func ParseCIDR(v string) (netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return netip.Prefix{}, errors.New("address is empty")
	}
	if strings.Contains(v, "/") {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("malformed CIDR %q", v)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("malformed address %q", v)
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Normalise coerces every asset's params to the types its catalog entry declares:
// unknown keys are dropped, missing keys are filled from defaults, and values are
// converted to the declared type. Run this after Validate and before generating —
// it is what stops arbitrary JSON reaching the HCL writer.
//
// It also guarantees every slice is non-nil. That matters more than it looks: a
// nil slice marshals to JSON `null`, and the browser treats these as arrays, so
// one nil rule list is enough to break line drawing for the whole chart.
func Normalise(s *Session) {
	s.Version = SchemaVersion
	s.Accounts = orEmpty(s.Accounts)
	s.ExternalIPs = orEmpty(s.ExternalIPs)
	s.Connections = orEmpty(s.Connections)
	for i := range s.Connections {
		s.Connections[i].AToB = orEmpty(s.Connections[i].AToB)
		s.Connections[i].BToA = orEmpty(s.Connections[i].BToA)
	}
	for i := range s.Accounts {
		s.Accounts[i].Assets = orEmpty(s.Accounts[i].Assets)
	}
	for i := range s.Accounts {
		acc := &s.Accounts[i]
		for j := range acc.Assets {
			as := &acc.Assets[j]
			t, ok := catalog.Type(acc.Provider, as.Code)
			if !ok {
				continue
			}
			clean := make(map[string]any, len(t.Params))
			for _, f := range t.Params {
				key := f.ParamKey()
				clean[key] = coerce(f, as.Params[key])
			}
			as.Params = clean
		}
	}
	for i := range s.ExternalIPs {
		if p, err := ParseCIDR(s.ExternalIPs[i].IP); err == nil {
			s.ExternalIPs[i].IP = p.String()
		}
	}
	// Every spelling of "all ports" is stored the same way, so a rule written as
	// "any" and one written as "*" are the same rule. Blank is left blank: it means
	// unspecified, and rewriting it to a wildcard would be the fail-open behaviour
	// this exists to avoid.
	for i := range s.Connections {
		for _, rules := range [][]Rule{s.Connections[i].AToB, s.Connections[i].BToA} {
			for j := range rules {
				if isAnyPort(rules[j].Port) {
					rules[j].Port = AnyPortValue
				}
			}
		}
	}
}

// coerce converts one incoming param value to the field's declared type, falling
// back to the default when the value is missing or unusable.
func coerce(f catalog.ParamField, v any) any {
	if v == nil {
		return f.Default
	}
	switch f.Type {
	case catalog.FieldBoolean:
		b, ok := v.(bool)
		if !ok {
			return f.Default
		}
		return b
	case catalog.FieldNumber:
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case string:
			if parsed, err := strconv.ParseFloat(n, 64); err == nil {
				return parsed
			}
		}
		return f.Default
	case catalog.FieldSelect:
		s, ok := v.(string)
		if !ok {
			return f.Default
		}
		for _, opt := range f.Options {
			if opt == s {
				return s
			}
		}
		return f.Default
	case catalog.FieldScript:
		// Newlines are the point here. Null bytes are not, and would corrupt the
		// generated file.
		s, ok := v.(string)
		if !ok || len(s) > maxScriptLen || strings.ContainsRune(s, 0) {
			return f.Default
		}
		return s
	default:
		s, ok := v.(string)
		if !ok || len(s) > maxNameLen || strings.ContainsAny(s, "\x00\n\r") {
			return f.Default
		}
		return s
	}
}

// orEmpty replaces a nil slice with an empty one, so it marshals to `[]` rather
// than `null`.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Ports parses a rule's port field. any is true when the rule covers all ports.
//
// Validate has already rejected malformed values, so a parse failure here is
// treated as "any" rather than returning an error nobody can act on.
func (r Rule) Ports() (lo, hi int, any bool) {
	lo, hi, any, err := parsePort(r.Port)
	if err != nil {
		return 0, 0, true
	}
	return lo, hi, any
}

// OverrideCIDR returns the CIDR a rule's Detail field carries, if any. This is
// the documented escape hatch for endpoints infrachart does not manage: a Detail
// containing "/" is treated as an explicit address rather than a note.
func (r Rule) OverrideCIDR() (string, bool) {
	if !strings.Contains(r.Detail, "/") {
		return "", false
	}
	p, err := ParseCIDR(r.Detail)
	if err != nil {
		return "", false
	}
	return p.String(), true
}
