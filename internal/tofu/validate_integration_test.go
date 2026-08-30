//go:build integration

package tofu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"infrachart/internal/model"
)

// The acceptance gate. Runs the real thing against real provider schemas, which
// is the only check that proves an argument exists — `fmt` proves only that the
// syntax parses, and that is exactly how fifteen invented parameters survived.
//
// Behind a build tag because it downloads providers and takes tens of seconds:
//
//	go test -tags integration ./internal/tofu
func TestValidateAgainstRealProviders(t *testing.T) {
	bin := ""
	for _, candidate := range []string{"tofu", "terraform"} {
		if path, err := exec.LookPath(candidate); err == nil {
			bin = path
			break
		}
	}
	if bin == "" {
		t.Skip("neither tofu nor terraform is installed")
	}

	for _, tc := range []struct {
		name    string
		session model.Session
	}{
		// Every registered type, so nothing is unchecked.
		{"everytype", model.EveryType()},
		// The demonstration chart, which unlike EveryType has connections and so
		// exercises the firewall rules and their attachments.
		{"seed", model.Seed()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hcl, _ := generate(t, tc.session)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"init", "-no-color", "-input=false"},
				{"validate", "-no-color"},
			} {
				cmd := exec.Command(bin, args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s failed:\n%s", args[0], out)
				}
			}
		})
	}
}

// The red path. A generated chart validates, so the failure case needs
// configuration that genuinely does not — otherwise nothing proves the endpoint
// can report a failure at all.
func TestValidateReportsFailure(t *testing.T) {
	broken := `
terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}

provider "aws" {
  region = "eu-west-2"
}

resource "aws_instance" "broken" {
  # instance_type is required and absent, and this argument does not exist.
  not_a_real_argument = true
}
`
	got := Validate(context.Background(), broken)
	if got.OK {
		t.Fatal("invalid configuration reported as valid")
	}
	if got.Tool == "" {
		t.Skip("no tool installed")
	}
	if !strings.Contains(got.Output, "not_a_real_argument") {
		t.Errorf("failure output does not name the problem:\n%s", got.Output)
	}
}

// A chart that validates must come back clean, or the green path is untested.
func TestValidateReportsSuccess(t *testing.T) {
	hcl, _ := generate(t, model.Seed())
	got := Validate(context.Background(), hcl)
	if got.Tool == "" {
		t.Skip("no tool installed")
	}
	if !got.OK {
		t.Fatalf("generated configuration failed validation:\n%s", got.Output)
	}
}
