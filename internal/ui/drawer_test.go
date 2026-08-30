package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
	"infrachart/internal/tofu"
)

// The drawer shows each direction's results beside its rules, so picking the wrong
// direction would attach the explanation to the wrong list — and quietly tell the
// user the opposite of the truth.
func TestOutcomesFollowDirection(t *testing.T) {
	s := model.Seed()
	model.Normalise(&s)
	report := tofu.Classify(s)

	// conn_5 is the egress-only connection: the web tier may reach the internet,
	// and nothing may come back.
	var egress model.Connection
	for _, c := range s.Connections {
		if c.ID == "conn_5" {
			egress = c
		}
	}
	if egress.ID == "" {
		t.Fatal("fixture moved: conn_5 is missing")
	}

	if got := outcomes(report, egress, "aToB"); len(got) != 1 {
		t.Errorf("aToB has %d outcomes, want 1 — the direction the chart declares", len(got))
	}
	if got := outcomes(report, egress, "bToA"); len(got) != 0 {
		t.Errorf("bToA has %d outcomes, want 0 — that direction is blocked", len(got))
	}
}

// An actionable refusal and an informational note must not look the same, or the
// refusals get lost among the notes.
func TestNoteStylingSeparatesActionable(t *testing.T) {
	action := tofu.Outcome{Strategy: tofu.StratRefused, Reason: "no stable address"}
	info := tofu.Outcome{Strategy: tofu.StratIAM, Reason: "governed by IAM"}

	if noteClass(action) == noteClass(info) {
		t.Error("a refusal and an informational note render identically")
	}
	if !strings.Contains(noteText(action), "No rule will be generated") {
		t.Errorf("a refusal does not say so: %q", noteText(action))
	}
}

func TestOutcomeAtBounds(t *testing.T) {
	results := []tofu.Outcome{{Reason: "first"}}
	if outcomeAt(results, 0) == nil {
		t.Error("index 0 returned nil")
	}
	for _, i := range []int{-1, 1, 99} {
		if outcomeAt(results, i) != nil {
			t.Errorf("index %d should be nil", i)
		}
	}
}

// Directives change what gets generated, so hiding one behind the disclosure
// would bury a real decision — the static address toggle is the current example.
func TestSplitParamsKeepsDirectivesVisible(t *testing.T) {
	fields := []catalog.ParamField{
		{Key: "instance_type"},
		{Key: "root_volume_gb", Advanced: true},
		{Key: catalog.ParamStaticPublicIP, Advanced: true, Directive: true},
	}
	essential, advanced := splitParams(fields)

	if len(advanced) != 1 || advanced[0].Key != "root_volume_gb" {
		t.Errorf("advanced = %v, want only root_volume_gb", keys(advanced))
	}
	if len(essential) != 2 {
		t.Errorf("essential = %v, want the instance type and the directive", keys(essential))
	}
	for _, f := range advanced {
		if f.Directive {
			t.Errorf("directive %q was hidden behind the disclosure", f.Key)
		}
	}
}

// Order within each group must survive the split, since the catalog declares
// fields in the order they should be read.
func TestSplitParamsPreservesOrder(t *testing.T) {
	fields := []catalog.ParamField{
		{Key: "a"}, {Key: "x", Advanced: true}, {Key: "b"}, {Key: "y", Advanced: true},
	}
	essential, advanced := splitParams(fields)
	if got := keys(essential); got != "a,b" {
		t.Errorf("essential order = %s", got)
	}
	if got := keys(advanced); got != "x,y" {
		t.Errorf("advanced order = %s", got)
	}
}

func keys(fields []catalog.ParamField) string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Key)
	}
	return strings.Join(out, ",")
}

// End-to-end: a type with advanced fields must actually render the disclosure with
// the count in its summary. Registered as its own provider so the real catalog
// entries are untouched.
func TestDrawerRendersDisclosure(t *testing.T) {
	// The registry is global, so this must not outlive the test.
	t.Cleanup(func() { catalog.Unregister("testcloud") })
	catalog.Register(catalog.Provider{
		Key: "testcloud", Label: "TestCloud", Color: "#888888", Dim: "#333333",
		TofuLocalName: "testcloud", TofuSource: "test/testcloud",
		Types: []catalog.ResourceType{{
			Code: "TT", Name: "Test thing", TofuType: "testcloud_thing",
			Network: catalog.NetFirewalled,
			Params: []catalog.ParamField{
				{Key: "size", Label: "Size", Type: catalog.FieldText, Default: "small"},
				{Key: "volume", Label: "Volume", Type: catalog.FieldNumber, Default: 10, Advanced: true},
				{Key: "encrypted", Label: "Encrypted", Type: catalog.FieldBoolean, Default: true, Advanced: true},
			},
		}},
	})

	s := model.Session{
		Version: model.SchemaVersion,
		Accounts: []model.Account{{
			ID: "acc_t", Name: "Test", Provider: "testcloud",
			Assets: []model.Asset{{
				ID: "asset_t", Code: "TT", Name: "Thing",
				Params: catalog.Defaults("testcloud", "TT"),
			}},
		}},
	}
	model.Normalise(&s)

	var buf bytes.Buffer
	sel := Selection{Kind: "asset", AccountID: "acc_t", AssetID: "asset_t"}
	if err := Drawer(s, sel).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, "Advanced options (2)") {
		t.Error("disclosure summary missing or miscounted")
	}
	// The essential field must sit outside the disclosure, or the split did
	// nothing useful.
	before, after, found := strings.Cut(got, "<details")
	if !found {
		t.Fatal("no <details> element rendered")
	}
	if !strings.Contains(before, `data-param="size"`) {
		t.Error("essential field was not rendered before the disclosure")
	}
	for _, key := range []string{"volume", "encrypted"} {
		if !strings.Contains(after, `data-param="`+key+`"`) {
			t.Errorf("advanced field %q was not rendered inside the disclosure", key)
		}
	}
}

// A script needs a textarea; a single-line input would silently drop everything
// after the first newline when the browser posted it back.
func TestScriptFieldRendersATextarea(t *testing.T) {
	t.Cleanup(func() { catalog.Unregister("scriptcloud") })
	catalog.Register(catalog.Provider{
		Key: "scriptcloud", Label: "ScriptCloud", Color: "#666666", Dim: "#222222",
		TofuLocalName: "scriptcloud", TofuSource: "test/scriptcloud",
		NameArg: "name",
		Types: []catalog.ResourceType{{
			Code: "SC", Name: "Script thing", TofuType: "scriptcloud_thing",
			Network: catalog.NetFirewalled,
			Params: []catalog.ParamField{
				{Key: "user_data", Label: "Startup script", Type: catalog.FieldScript, Default: ""},
			},
		}},
	})

	s := model.Session{
		Version: model.SchemaVersion,
		Accounts: []model.Account{{
			ID: "acc_s", Name: "Scripts", Provider: "scriptcloud",
			Assets: []model.Asset{{
				ID: "asset_s", Code: "SC", Name: "Thing",
				Params: map[string]any{"user_data": "#!/bin/bash\necho hello\n"},
			}},
		}},
	}
	model.Normalise(&s)

	var buf bytes.Buffer
	sel := Selection{Kind: "asset", AccountID: "acc_s", AssetID: "asset_s"}
	if err := Drawer(s, sel).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, `<textarea`) {
		t.Error("script field did not render a textarea")
	}
	if !strings.Contains(got, `data-param="user_data"`) {
		t.Error("textarea is not wired to the param")
	}
	// The whole script must be present, not just its first line.
	if !strings.Contains(got, "echo hello") {
		t.Errorf("script body was truncated:\n%s", got)
	}
}
