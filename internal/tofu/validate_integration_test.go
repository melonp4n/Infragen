//go:build integration

package tofu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"infrachart/internal/catalog"
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
		// Every type that can have a durable address, with one. The attachment
		// differs per cloud — an EIP association, a NIC argument, an access_config
		// block — and only the real tool proves each of those arguments exists.
		{"everytype-static", staticEverywhere()},
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

// The regression the newline escaping exists for. A literal newline inside an HCL
// quoted string is a parse error, and only the real tool proves the escaping is
// right — a golden file would happily record broken output.
func TestUserDataValidates(t *testing.T) {
	script := "#!/bin/bash\nset -euo pipefail\n# a \"quoted\" ${thing} and a %{directive}\napt-get update\n"
	s := model.Seed()
	s.Accounts[0].Assets[2].Params["user_data"] = script   // AWS EC2
	s.Accounts[2].Assets[0].Params["user_data"] = script   // DigitalOcean droplet
	s.Accounts[3].Assets[0].Params["custom_data"] = script // Azure VM

	hcl, _ := generate(t, s)
	got := Validate(context.Background(), hcl)
	if got.Tool == "" {
		t.Skip("no tool installed")
	}
	if !got.OK {
		t.Fatalf("a chart with startup scripts failed validation:\n%s", got.Output)
	}
}

// staticEverywhere turns on the durable address, and Ansible, for every type that
// supports one. Ansible comes with it because the inventory is what reads the
// address back, so a wrongly wired attachment shows up as a broken reference.
func staticEverywhere() model.Session {
	s := model.EveryType()
	for i := range s.Accounts {
		acc := &s.Accounts[i]
		for j := range acc.Assets {
			a := &acc.Assets[j]
			rt, ok := catalog.Type(acc.Provider, a.Code)
			if !ok || rt.StaticAddr == nil {
				continue
			}
			a.Params[catalog.ParamStaticPublicIP] = true
			a.Params[catalog.ParamAnsible] = true
			a.Params[catalog.ParamAnsibleGroup] = "web"
		}
	}
	return s
}

// A chart with Ansible hosts must validate: the key pair companions, the metadata
// map on GCP, and the lifecycle preconditions are all new shapes that only the
// real tool checks.
func TestAnsibleChartValidates(t *testing.T) {
	s := model.Seed()
	for _, target := range []struct{ acc, asset int }{
		{0, 2}, // AWS EC2
		{2, 0}, // DigitalOcean droplet
		{3, 0}, // Azure VM
	} {
		a := &s.Accounts[target.acc].Assets[target.asset]
		a.Params[catalog.ParamAnsible] = true
		a.Params[catalog.ParamAnsibleGroup] = "web"
		a.Params[catalog.ParamStaticPublicIP] = true
	}
	hcl, _ := generate(t, s)

	got := Validate(context.Background(), hcl)
	if got.Tool == "" {
		t.Skip("no tool installed")
	}
	if !got.OK {
		t.Fatalf("an Ansible chart failed validation:\n%s", got.Output)
	}
}

// The check `tofu validate` cannot do.
//
// Validation loads provider schemas but never executes a data source, so it
// proves most_recent, owners and filter are real arguments and nothing about
// whether a name pattern matches a published image. A pattern that matches
// nothing fails at apply with "Your query returned no results" — loud, but only
// once someone is deploying.
//
// Skips without the AWS CLI or credentials, so it costs nothing in an environment
// that cannot answer the question.
func TestAMIFiltersResolve(t *testing.T) {
	if _, err := exec.LookPath("aws"); err != nil {
		t.Skip("aws CLI not installed")
	}
	if err := exec.Command("aws", "sts", "get-caller-identity").Run(); err != nil {
		t.Skip("no AWS credentials available")
	}
	// One region is enough to prove a pattern is well formed. A pattern valid here
	// and nowhere else would mean a region-specific image name, which none of these
	// are — that is the whole point of resolving by name instead of by ID.
	const region = "eu-west-2"

	for _, p := range catalog.AMIPresets() {
		t.Run(p.Label, func(t *testing.T) {
			if p.Exact() {
				out, err := exec.Command("aws", "ssm", "get-parameter",
					"--region", region, "--name", p.SSMPath,
					"--query", "Parameter.Value", "--output", "text").Output()
				if err != nil {
					t.Fatalf("parameter %s does not resolve in %s: %v", p.SSMPath, region, err)
				}
				// The parameter exists; it still has to hold an AMI ID rather than,
				// say, the JSON blob some of the ECS paths return.
				if got := strings.TrimSpace(string(out)); !strings.HasPrefix(got, "ami-") {
					t.Errorf("parameter %s returned %q, which is not an AMI ID", p.SSMPath, got)
				}
				return
			}
			out, err := exec.Command("aws", "ec2", "describe-images",
				"--region", region,
				"--owners", p.Owner,
				"--filters", "Name=name,Values="+p.Filter,
				"--query", "length(Images)", "--output", "text").Output()
			if err != nil {
				t.Fatalf("describe-images failed: %v", err)
			}
			if got := strings.TrimSpace(string(out)); got == "0" || got == "None" {
				t.Errorf("owner %s + filter %q matches no image in %s — an instance using this preset would fail at apply",
					p.Owner, p.Filter, region)
			}
		})
	}
}
