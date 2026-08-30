package tofu

import (
	"context"
	"strings"
	"testing"
)

// With no tool installed the result must be a failure carrying a message that
// says what to do, not an empty red panel.
func TestValidateWithoutATool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := Validate(context.Background(), "")
	if got.OK {
		t.Error("reported success with no tool installed")
	}
	if !strings.Contains(got.Output, "on PATH") {
		t.Errorf("output does not say what is missing: %q", got.Output)
	}
	if got.Tool != "" {
		t.Errorf("named a tool that was not found: %q", got.Tool)
	}
}
