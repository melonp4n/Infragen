package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"infrachart/internal/catalog"
)

// The seed chart is the fixture: it covers every node type, every line kind and
// both rule directions.
func TestSeedIsValid(t *testing.T) {
	s := Seed()
	if err := Validate(&s); err != nil {
		t.Fatalf("seed session failed validation: %v", err)
	}
}

// Export and re-import must be lossless. Stable ids across a round-trip are what
// a future drift checker will rely on to map a cloud resource back to a node.
func TestRoundTrip(t *testing.T) {
	original := Seed()
	Normalise(&original)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Session
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := Validate(&back); err != nil {
		t.Fatalf("round-tripped session failed validation: %v", err)
	}
	Normalise(&back)

	if !reflect.DeepEqual(original, back) {
		t.Error("session changed across a JSON round-trip")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Session){
		"dangling asset reference": func(s *Session) {
			s.Connections[0].B = NodeRef{Type: NodeAsset, AccountID: "acc_1", AssetID: "nope"}
		},
		"dangling external ip": func(s *Session) {
			s.Connections[6].A = NodeRef{Type: NodeExtIP, ID: "nope"}
		},
		"unknown provider":      func(s *Session) { s.Accounts[0].Provider = "oracle" },
		"unknown resource code": func(s *Session) { s.Accounts[0].Assets[0].Code = "NOPE" },
		"duplicate id":          func(s *Session) { s.Accounts[1].ID = s.Accounts[0].ID },
		"empty id":              func(s *Session) { s.Accounts[0].ID = "" },
		"malformed external ip": func(s *Session) { s.ExternalIPs[0].IP = "203.0.113.999" },
		"malformed prefix":      func(s *Session) { s.ExternalIPs[0].IP = "203.0.113.10/48" },
		"unknown protocol":      func(s *Session) { s.Connections[0].AToB[0].Protocol = "SCTP" },
		"port out of range":     func(s *Session) { s.Connections[0].AToB[0].Port = "70000" },
		"inverted port range":   func(s *Session) { s.Connections[0].AToB[0].Port = "900-100" },
		"non-numeric port":      func(s *Session) { s.Connections[0].AToB[0].Port = "http" },
		"self connection":       func(s *Session) { s.Connections[1].B = s.Connections[1].A },
		"unsupported version":   func(s *Session) { s.Version = 99 },
		"newline in name":       func(s *Session) { s.Accounts[0].Name = "a\nb" },
		"unknown node type":     func(s *Session) { s.Connections[0].A = NodeRef{Type: "vpc"} },
	}

	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			s := Seed()
			corrupt(&s)
			if err := Validate(&s); err == nil {
				t.Error("expected validation to reject this session, but it passed")
			}
		})
	}
}

// Normalise is the boundary that keeps arbitrary JSON out of generated HCL, so
// it has to drop what the catalog does not declare and repair what it does.
func TestNormaliseCoercesParams(t *testing.T) {
	s := Seed()
	ec2 := &s.Accounts[0].Assets[2]
	if ec2.Code != "EC2" {
		t.Fatalf("fixture moved: expected EC2, got %q", ec2.Code)
	}
	ec2.Params = map[string]any{
		"instance_type":                 "m9.enormous", // not in the option list
		"root_block_device.volume_size": "40",          // a number arriving as a string
		"associate_public_ip_address":   "yes",         // not a boolean
		"injected":                      "rm -rf /",    // not in the schema at all
		// "ami_os" omitted entirely
	}
	Normalise(&s)

	got := ec2.Params
	if _, ok := got["injected"]; ok {
		t.Error("undeclared param survived normalisation")
	}
	if got["instance_type"] != "t3.micro" {
		t.Errorf("out-of-list select value kept: %v", got["instance_type"])
	}
	if got["root_block_device.volume_size"] != float64(40) {
		t.Errorf("numeric string not coerced: %#v", got["root_block_device.volume_size"])
	}
	if got["associate_public_ip_address"] != false {
		t.Errorf("non-boolean not replaced by default: %#v", got["associate_public_ip_address"])
	}
	if got[catalog.ParamAMIOS] != "Amazon Linux 2023" {
		t.Errorf("missing param not filled from default: %#v", got[catalog.ParamAMIOS])
	}
}

// A bare address is stored with an explicit mask so generated rules never carry
// an implicit one.
func TestNormaliseMasksAddresses(t *testing.T) {
	s := Seed()
	s.ExternalIPs[0].IP = "203.0.113.10"
	Normalise(&s)
	if s.ExternalIPs[0].IP != "203.0.113.10/32" {
		t.Errorf("bare address not masked: %q", s.ExternalIPs[0].IP)
	}
}

func TestNodeRefKey(t *testing.T) {
	cases := map[string]NodeRef{
		"internet":            {Type: NodeInternet},
		"extip:extip_1":       {Type: NodeExtIP, ID: "extip_1"},
		"asset:acc_1:asset_3": {Type: NodeAsset, AccountID: "acc_1", AssetID: "asset_3"},
	}
	for want, ref := range cases {
		if got := ref.Key(); got != want {
			t.Errorf("Key() = %q, want %q", got, want)
		}
	}
}

// A nil slice marshals to JSON `null`, and the browser treats these as arrays.
// One nil rule list breaks line drawing for the whole chart, so Normalise has to
// leave every slice non-nil.
func TestNormaliseLeavesNoNullSlices(t *testing.T) {
	s := Seed() // Seed passes nil for every empty rule list
	Normalise(&s)

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"accounts", "externalIps", "connections"} {
		if raw[key] == nil {
			t.Errorf("session.%s marshalled as null", key)
		}
	}
	for i, c := range raw["connections"].([]any) {
		conn := c.(map[string]any)
		for _, dir := range []string{"aToB", "bToA"} {
			if conn[dir] == nil {
				t.Errorf("connections[%d].%s marshalled as null", i, dir)
			}
		}
	}
	for i, a := range raw["accounts"].([]any) {
		if a.(map[string]any)["assets"] == nil {
			t.Errorf("accounts[%d].assets marshalled as null", i)
		}
	}
}

// "All ports" has to be reachable by typing something, and blank is not it.
func TestAnyPortSpellings(t *testing.T) {
	for _, port := range []string{"*", "any", "ANY", "all", " * "} {
		s := Seed()
		s.Connections[0].AToB[0].Port = port
		if err := Validate(&s); err != nil {
			t.Errorf("port %q rejected: %v", port, err)
			continue
		}
		Normalise(&s)

		// One stored spelling, so two rules meaning the same thing compare equal.
		if got := s.Connections[0].AToB[0].Port; got != AnyPortValue {
			t.Errorf("port %q normalised to %q, want %q", port, got, AnyPortValue)
		}
		if _, _, any := s.Connections[0].AToB[0].Ports(); !any {
			t.Errorf("port %q did not parse as all ports", port)
		}
	}
}

// The important one. A blank port must never mean "all ports": a rule someone
// abandoned half-written would otherwise open every port on the asset.
func TestBlankPortIsNotAWildcard(t *testing.T) {
	s := Seed()
	s.Connections[0].AToB[0].Port = ""

	// Accepted, so the drawer can still render a half-written rule...
	if err := Validate(&s); err != nil {
		t.Fatalf("blank port rejected during editing: %v", err)
	}
	Normalise(&s)
	if got := s.Connections[0].AToB[0].Port; got != "" {
		t.Errorf("blank port was rewritten to %q — that is the fail-open behaviour", got)
	}

	// ...but it is not a wildcard, and it says so.
	if _, _, any := s.Connections[0].AToB[0].Ports(); any {
		t.Error("blank port parsed as all ports")
	}
	if !s.Connections[0].AToB[0].PortUnset() {
		t.Error("blank port not reported as unset")
	}
}

// Port 0 stays rejected: it is a real value in some protocols, so treating it as
// a wildcard would make a typo mean "everything". The message must say what to
// type instead.
func TestPortZeroRejectedWithGuidance(t *testing.T) {
	for _, port := range []string{"0", "70000", "900-100", "http"} {
		s := Seed()
		s.Connections[0].AToB[0].Port = port
		err := Validate(&s)
		if err == nil {
			t.Errorf("port %q accepted", port)
			continue
		}
		if !strings.Contains(err.Error(), `"*"`) {
			t.Errorf("port %q error does not say what to type instead: %v", port, err)
		}
	}
}

// Validation and generation must agree on what a port field means. They used to
// each carry their own copy of the rule, and disagreed: one rejected a value the
// other silently read as "all ports".
func TestValidationAndParsingAgree(t *testing.T) {
	for _, port := range []string{"", "*", "443", "8000-8080", "0", "junk", "70000"} {
		s := Seed()
		s.Connections[0].AToB[0].Port = port
		accepted := Validate(&s) == nil

		rule := s.Connections[0].AToB[0]
		lo, hi, any := rule.Ports()

		switch {
		case rule.PortUnset():
			// Unset is accepted while editing and generates nothing, so it must be
			// neither a wildcard nor a range.
			if any || lo != 0 || hi != 0 {
				t.Errorf("port %q is unset but parsed as %d-%d any=%v", port, lo, hi, any)
			}
		case accepted && !any && (lo == 0 || hi == 0):
			t.Errorf("port %q was accepted but parses to an empty range", port)
		case !accepted && !any && lo != 0:
			t.Errorf("port %q was rejected but parses as a concrete range %d-%d", port, lo, hi)
		}
	}
}

// A startup script is the one param type that may contain newlines. Names and
// notes still may not — that guard is what keeps a control character out of a
// resource name.
func TestScriptParamsAllowNewlines(t *testing.T) {
	script := catalog.ParamField{Key: "user_data", Type: catalog.FieldScript, Default: ""}
	name := catalog.ParamField{Key: "ami", Type: catalog.FieldText, Default: "fallback"}

	body := "#!/bin/bash\nset -euo pipefail\napt-get update\n"
	if got := coerce(script, body); got != body {
		t.Errorf("script was rejected or altered: %q", got)
	}
	if got := coerce(name, "a\nb"); got != "fallback" {
		t.Errorf("a newline in a text param was kept: %q", got)
	}

	// A null byte would corrupt the generated file whatever the field type.
	if got := coerce(script, "ok\x00bad"); got != "" {
		t.Errorf("null byte survived in a script: %q", got)
	}
	// Scripts get a much larger cap than names, but still a cap.
	if got := coerce(script, strings.Repeat("x", maxScriptLen+1)); got != "" {
		t.Error("an oversized script was accepted")
	}
	if got := coerce(script, strings.Repeat("x", maxNameLen+1)); got == "" {
		t.Error("a script longer than a name was rejected — the caps are not separate")
	}
}
