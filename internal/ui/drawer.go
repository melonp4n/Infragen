package ui

import (
	"fmt"
	"strconv"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
	"infrachart/internal/tofu"
)

// flagInfo is the coloured badge under the drawer title. It is the fastest read
// on the panel, so it says the one thing that matters: whether this connection
// is public.
type flagInfo struct {
	Class string
	Text  string
}

func findConn(s model.Session, id string) *model.Connection {
	for i := range s.Connections {
		if s.Connections[i].ID == id {
			return &s.Connections[i]
		}
	}
	return nil
}

// connFlag classifies a connection for the drawer header.
func connFlag(s model.Session, c model.Connection) flagInfo {
	inbound, outbound, isInternet := c.InternetTraffic()
	if !isInternet {
		if c.A.Type == model.NodeExtIP || c.B.Type == model.NodeExtIP {
			return flagInfo{Class: "flag-extip", Text: "hardcoded external address · not deployed"}
		}
		return flagInfo{Class: "flag-internal", Text: "internal connection"}
	}
	switch {
	case len(inbound) > 0:
		return flagInfo{Class: "flag-danger", Text: "⚠ publicly reachable — the internet can initiate to this asset"}
	case len(outbound) > 0:
		return flagInfo{Class: "flag-warning", Text: "↗ outbound only — asset can reach the internet, not reachable from it"}
	default:
		return flagInfo{Class: "flag-muted", Text: "no internet rules defined yet"}
	}
}

// directionClass tints a rule list by what it means: red for traffic arriving
// from the internet, amber for traffic heading out to it.
func directionClass(from, to model.NodeRef) string {
	if from.Type == model.NodeInternet {
		return "rst-danger"
	}
	if to.Type == model.NodeInternet {
		return "rst-warning"
	}
	return ""
}

func directionHint(s model.Session, from, to model.NodeRef) string {
	if from.Type == model.NodeInternet {
		return "public ingress — internet reaching in"
	}
	if to.Type == model.NodeInternet {
		return "outbound egress — leaving to the internet"
	}
	return "traffic " + s.Label(to) + " will accept, initiated by " + s.Label(from)
}

// inputType maps a catalog field type to an HTML input type.
func inputType(fieldType string) string {
	if fieldType == catalog.FieldNumber {
		return "number"
	}
	return "text"
}

// text renders a param value for display. Numbers arrive from JSON as float64,
// so whole numbers must not come back as "20.000000".
func text(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(n)
	default:
		return fmt.Sprint(n)
	}
}

func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}

// outcomes returns the classified results for one direction of a connection, in
// the same order as its rules.
func outcomes(r tofu.Report, c model.Connection, dir string) []tofu.Outcome {
	from := c.A
	if dir != "aToB" {
		from = c.B
	}
	f, ok := r.FlowFor(c.ID, from)
	if !ok {
		return nil
	}
	return f.Outcomes
}

func outcomeAt(results []tofu.Outcome, i int) *tofu.Outcome {
	if i < 0 || i >= len(results) {
		return nil
	}
	return &results[i]
}

// noteClass separates a rule that needs the user to do something from one that is
// merely worth explaining. Styling them the same would bury the actionable ones.
func noteClass(o tofu.Outcome) string {
	if o.NeedsAction() {
		return "rule-note-action"
	}
	return "rule-note-info"
}

func noteText(o tofu.Outcome) string {
	if o.Strategy == tofu.StratRefused {
		return "No rule will be generated — " + o.Reason
	}
	return o.Reason
}

// splitParams divides a type's fields into those shown immediately and those
// behind the disclosure. Directives stay with the essential set: they change what
// gets generated, so hiding them would bury a real decision.
//
// params is the asset's own values, needed because a field gated by RequiresParam
// is not shown at all when its gate is off. Rendering one would invite a value
// that generation then drops, which is worse than not offering it.
func splitParams(fields []catalog.ParamField, params map[string]any) (essential, advanced []catalog.ParamField) {
	for _, f := range fields {
		if f.RequiresParam != "" && !truthy(params[f.RequiresParam]) {
			continue
		}
		if f.Advanced && !f.Directive {
			advanced = append(advanced, f)
			continue
		}
		essential = append(essential, f)
	}
	return essential, advanced
}
