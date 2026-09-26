package tofu

import (
	"strings"
	"testing"
)

func TestGCPProjectReachesTheVariable(t *testing.T) {
	s := inboundChart("gcp", "GCE")
	s.Accounts[0].Params = map[string]any{"project": "red-team-ops-1234", "region": "us-east4"}

	hcl, warnings := generate(t, s)
	flat := collapse(hcl)
	if !strings.Contains(flat, `default = "red-team-ops-1234"`) {
		t.Errorf("the account project did not reach its variable:\n%s", hcl)
	}
	if !strings.Contains(flat, `default = "us-east4"`) {
		t.Errorf("the account region did not reach its variable:\n%s", hcl)
	}
	for _, w := range warnings {
		if strings.Contains(w.Text, "Project ID") {
			t.Errorf("warned about a project that is set: %s", w.Text)
		}
	}
}

func TestBlankGCPProjectIsWarnedAbout(t *testing.T) {
	_, warnings := generate(t, inboundChart("gcp", "GCE"))
	for _, w := range warnings {
		if strings.Contains(w.Text, "Project ID") {
			return
		}
	}
	t.Errorf("a GCP account with no project drew no warning: %v", warnings)
}
