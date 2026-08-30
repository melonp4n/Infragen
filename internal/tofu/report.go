package tofu

import (
	"fmt"
	"strings"
)

// Text renders the classification for a human. It is not OpenTofu — emission is
// the next step. Until then this is the useful half: it shows which connections
// will become firewall rules, which become something else, and which cannot be
// expressed at all.
func (r Report) Text() string {
	var b strings.Builder

	b.WriteString("# infrachart — connection plan\n")
	b.WriteString("# What each connection becomes. OpenTofu emission is not built yet.\n\n")

	if w := r.Warnings(); len(w) > 0 {
		fmt.Fprintf(&b, "## Needs your attention (%d)\n\n", len(w))
		for _, line := range w {
			fmt.Fprintf(&b, "  ! %s\n", line)
		}
		b.WriteString("\n")
	}

	if len(r.Flows) == 0 {
		b.WriteString("## Connections\n\n  No allowed traffic declared yet — draw a connection and add a rule.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "## Connections (%d directions)\n", len(r.Flows))
	for _, f := range r.Flows {
		fmt.Fprintf(&b, "\n%s  ->  %s\n", f.From.Label, f.To.Label)
		for _, o := range f.Outcomes {
			port := o.Rule.Port
			if port == "" {
				port = "(unset)"
			}
			line := fmt.Sprintf("  %-9s %s", o.Rule.Protocol+"/"+port, o.Strategy)
			if o.Source != "" {
				line = fmt.Sprintf("  %-9s %-19s %s", o.Rule.Protocol+"/"+port, o.Strategy, o.Source)
			}
			fmt.Fprintf(&b, "%s\n", line)
			if o.Reason != "" {
				fmt.Fprintf(&b, "            %s\n", o.Reason)
			}
		}
	}
	return b.String()
}
