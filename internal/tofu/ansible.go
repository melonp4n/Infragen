package tofu

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// Ansible support: which hosts are managed, what key reaches them, and what is
// wrong with the chart before any of it can work.
//
// infrachart never generates a key — see documentation/TOFU-MAPPING.md for the
// ephemeral investigation that ruled out the alternative.

// ansibleOn reports whether an asset is managed with Ansible.
func ansibleOn(a *model.Asset) bool {
	if a == nil {
		return false
	}
	on, _ := a.Params[catalog.ParamAnsible].(bool)
	return on
}

// param reads a directive's string value.
func param(a *model.Asset, key string) string {
	if a == nil {
		return ""
	}
	s, _ := a.Params[key].(string)
	return strings.TrimSpace(s)
}

// sshKeyExpr builds the expression that resolves an asset's public key.
//
// Four sources, first non-empty wins. The two per-resource ones are chart fields
// and resolve to literals here; the account and deployment ones are Terraform
// variables, because their values are not knowable at generation time.
func sshKeyExpr(accountID string, a *model.Asset) string {
	var sources []string
	if pasted := param(a, catalog.ParamSSHPublicKey); pasted != "" {
		sources = append(sources, quote(pasted))
	}
	if path := param(a, catalog.ParamSSHPublicKeyFile); path != "" {
		// file() fails at plan time on a wrong path, which is the right moment.
		sources = append(sources, fmt.Sprintf("file(%s)", quote(path)))
	}
	sources = append(sources,
		"var."+accountID+"_ssh_public_key",
		"var."+deploymentKeyVar,
		// A final empty fallback so coalesce cannot fail. A host with no key from
		// any source is a host nobody wants to SSH into, not an error.
		`""`,
	)
	return "coalesce(" + strings.Join(sources, ", ") + ")"
}

// loginUser is the account an SSH key is installed for. It defaults per provider
// image and is editable, because the stock login differs by distribution.
func loginUser(a *model.Asset) string {
	if u := param(a, catalog.ParamAnsibleUser); u != "" {
		return u
	}
	return "root"
}

// sshKeyLocal is the local holding an asset's resolved key.
func sshKeyLocal(assetID string) string { return "local." + assetID + "_ssh_key" }

// renderKeyLocals emits one local per asset that has key wiring.
//
// The local exists so that `count` and the key value can both read the same
// resolution without repeating a four-argument coalesce at every use.
func renderKeyLocals(w *writer, s model.Session) {
	type entry struct{ name, expr string }
	var entries []entry
	for _, acc := range s.Accounts {
		for _, a := range acc.Assets {
			rt, ok := catalog.Type(acc.Provider, a.Code)
			if !ok || !usesSSHKey(rt, a) {
				continue
			}
			entries = append(entries, entry{a.ID + "_ssh_key", sshKeyExpr(acc.ID, &a)})
		}
	}
	if len(entries) == 0 {
		return
	}
	w.blank()
	w.line("# Resolved SSH keys: per resource, then per account, then deployment-wide.")
	w.block("locals", func() {
		for _, e := range entries {
			w.arg(e.name, e.expr)
		}
	})
}

const deploymentKeyVar = "ssh_public_key"

// usesSSHKey reports whether this asset will actually emit a key reference.
//
// Checked rather than inferred from the Ansible flag: Azure's admin_ssh_key is
// unconditional, because every Linux VM needs one, so a plain Azure VM references
// the key variables without Ansible being involved at all. Declaring them from
// the flag left those references dangling.
func usesSSHKey(rt catalog.ResourceType, a model.Asset) bool {
	for _, fx := range rt.Fixed {
		if strings.Contains(fx.Expr, phSSHKey) && required(fx.RequiresParam, a) {
			return true
		}
	}
	for _, c := range rt.Companions {
		if !required(c.RequiresParam, a) {
			continue
		}
		for _, fx := range c.Fixed {
			if strings.Contains(fx.Expr, phSSHKey) {
				return true
			}
		}
	}
	return false
}

// sshKeyVars declares the account and deployment key variables for an asset.
// Both default to empty so neither prompts; a host left with no key at all is
// caught by its precondition at plan time instead.
func sshKeyVars(accountID, accountName string) []varDecl {
	return []varDecl{
		{
			Name: deploymentKeyVar, Type: "string", Default: `""`,
			Description: `SSH public key for every Ansible host. export TF_VAR_ssh_public_key="$(cat ~/.ssh/id_ed25519.pub)"`,
		},
		{
			Name: accountID + "_ssh_public_key", Type: "string", Default: `""`,
			Description: "SSH public key for Ansible hosts in " + accountName + ", overriding the deployment key",
		},
	}
}

// keyPrecondition is the plan-time error for a host with no key from any source.
//
// This is where "no key" is caught rather than at generation, because infrachart
// cannot see whether TF_VAR_ssh_public_key is set. The error names the asset and
// the command, which a generation-time guess could not.
func keyPrecondition(n *blockNode, accountID string, a *model.Asset) {
	if param(a, catalog.ParamSSHPublicKey) != "" || param(a, catalog.ParamSSHPublicKeyFile) != "" {
		return // a per-resource key is present, so the check would always pass
	}
	// Built with real quotes and escaped by quote(), rather than hand-escaped: the
	// message contains the asset name and a shell command, both full of quotes.
	msg := fmt.Sprintf(`Ansible is enabled on "%s" but no SSH public key is set. `+
		`Run: ssh-keygen -t ed25519 -f ./infrachart-ansible -N "" `+
		`&& export TF_VAR_ssh_public_key="$(cat ./infrachart-ansible.pub)"`, a.Name)

	n.add("lifecycle.precondition", "condition",
		fmt.Sprintf(`var.%s != "" || var.%s_ssh_public_key != ""`, deploymentKeyVar, accountID))
	n.add("lifecycle.precondition", "error_message", quote(msg))
}

// ---- inventory groups ------------------------------------------------------

// An INI section name. The group reaches a heredoc where quote() does not apply,
// so this is the injection surface — a name containing "]" or a newline could
// close the section and open another.
var groupName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

const ungrouped = "ungrouped"

// group returns the inventory group for an asset, and whether it was set.
func group(a *model.Asset) (string, bool) {
	g := param(a, catalog.ParamAnsibleGroup)
	if g == "" || !groupName.MatchString(g) {
		return ungrouped, false
	}
	return g, true
}

// ---- warnings --------------------------------------------------------------

// sshPort is what an Ansible host has to be reachable on.
const sshPort = 22

// hasSSHIngress reports whether any classified flow lets something reach this
// asset on port 22.
//
// An all-ports rule counts: a naive equality check against 22 would miss it and
// warn about a host that is in fact reachable.
func hasSSHIngress(r Report, assetID string) bool {
	for _, f := range r.Flows {
		if f.To.Asset == nil || f.To.Asset.ID != assetID {
			continue
		}
		for _, o := range f.Outcomes {
			if o.Strategy == StratRefused {
				continue // this rule generates nothing, so it grants no access
			}
			lo, hi, any := o.Rule.Ports()
			if any || (lo <= sshPort && sshPort <= hi) {
				return true
			}
		}
	}
	return false
}

// ansibleWarnings reports what stops an Ansible host from working. They join the
// existing warnings, so they reach the modal and the generated file unchanged.
func ansibleWarnings(s model.Session, r Report) []string {
	var out []string
	for _, acc := range s.Accounts {
		for i := range acc.Assets {
			a := &acc.Assets[i]
			if !ansibleOn(a) {
				continue
			}
			label := acc.Name + " → " + a.Name

			if !hasSSHIngress(r, a.ID) {
				out = append(out, fmt.Sprintf(
					"%s is managed with Ansible but nothing may reach it on port %d — "+
						"add a connection allowing SSH from your jump host or the internet", label, sshPort))
			}
			if _, ok := group(a); !ok {
				if raw := param(a, catalog.ParamAnsibleGroup); raw != "" {
					out = append(out, fmt.Sprintf(
						"%s has an Ansible group of %q, which is not a valid inventory section name — "+
							"letters, digits, dot, dash and underscore only. It will land in [%s]", label, raw, ungrouped))
				} else {
					out = append(out, fmt.Sprintf(
						"%s is managed with Ansible but has no group, so it lands in [%s]", label, ungrouped))
				}
			}
			if param(a, catalog.ParamSSHPublicKey) != "" && param(a, catalog.ParamSSHPublicKeyFile) != "" {
				out = append(out, fmt.Sprintf(
					"%s has both a pasted SSH key and a key file — the pasted one wins. "+
						"Clear one of them to say which you meant", label))
			}
			if expr, err := hostAddress(acc, a); err != nil {
				out = append(out, fmt.Sprintf("%s is managed with Ansible but %s", label, err))
			} else if expr == "" {
				out = append(out, fmt.Sprintf("%s is managed with Ansible but has no address to put in the inventory", label))
			}
		}
	}
	return out
}

// hostAddress resolves an asset to the address expression an inventory entry
// needs. It reuses the reachability rules that decide whether a firewall rule can
// name the host, because they are the same question.
func hostAddress(acc model.Account, a *model.Asset) (string, error) {
	rt, ok := catalog.Type(acc.Provider, a.Code)
	if !ok {
		return "", fmt.Errorf("its resource type is not in the catalog")
	}
	expr, reason := addressExpr(rt, a)
	if reason != "" {
		return "", fmt.Errorf("%s", reason)
	}
	return expr, nil
}

// ---- inventory -------------------------------------------------------------

// inventoryHost is one line of the inventory: an address expression and the login
// the provider's stock image creates.
type inventoryHost struct {
	Address string
	User    string
	Label   string
}

// inventory collects the Ansible hosts of a chart, grouped, in a stable order.
//
// Hosts with no usable address are left out rather than written as a broken line —
// ansibleWarnings has already said why for each of them.
func inventory(s model.Session) (groups []string, hosts map[string][]inventoryHost) {
	hosts = map[string][]inventoryHost{}
	for _, acc := range s.Accounts {
		for i := range acc.Assets {
			a := &acc.Assets[i]
			if !ansibleOn(a) {
				continue
			}
			addr, err := hostAddress(acc, a)
			if err != nil || addr == "" {
				continue
			}
			g, _ := group(a)
			if _, seen := hosts[g]; !seen {
				groups = append(groups, g)
			}
			user := param(a, catalog.ParamAnsibleUser)
			if user == "" {
				user = "root"
			}
			hosts[g] = append(hosts[g], inventoryHost{
				Address: addr,
				User:    user,
				Label:   acc.Name + " → " + a.Name,
			})
		}
	}
	sort.Strings(groups)
	return groups, hosts
}

// renderInventory emits the inventory as a local_file, written by Terraform at
// apply time.
//
// It cannot be a file infrachart writes: an inventory needs real addresses, and
// those do not exist until after apply.
func renderInventory(w *writer, s model.Session) bool {
	groups, hosts := inventory(s)
	if len(groups) == 0 {
		return false
	}

	var body strings.Builder
	body.WriteString("# Generated by infrachart. A skeleton — adjust it to your setup.\n")
	for _, g := range groups {
		fmt.Fprintf(&body, "\n[%s]\n", g)
		for _, h := range hosts[g] {
			fmt.Fprintf(&body, "# %s\n%s ansible_user=%s\n", h.Label, h.Address, h.User)
		}
	}
	body.WriteString("\n[all:vars]\n")
	body.WriteString("# Change this to the private key matching the public key you supplied.\n")
	body.WriteString("# infrachart never sees your private key, so this is a guess.\n")
	body.WriteString("ansible_ssh_private_key_file=~/.ssh/id_ed25519\n")

	w.blank()
	w.line("# Written at apply time, because an inventory needs real addresses and")
	w.line("# those do not exist until the resources are created.")
	w.block(fmt.Sprintf("resource %q %q", "local_file", "ansible_inventory"), func() {
		w.arg("filename", `"${path.cwd}/inventory.ini"`)
		w.arg("file_permission", quote("0600"))
		w.arg("content", heredoc(body.String()))
	})

	w.blank()
	w.line("# The inventory names every host and login; the state file holds more.")
	w.block(fmt.Sprintf("resource %q %q", "local_file", "gitignore"), func() {
		w.arg("filename", `"${path.cwd}/.gitignore"`)
		w.arg("content", heredoc("inventory.ini\nterraform.tfstate\nterraform.tfstate.backup\n.terraform/\n"))
	})
	return true
}

// heredoc renders a multi-line body as an indented HCL heredoc. Address
// expressions inside it are live interpolations, which is the point — a quoted
// string would need them escaped.
func heredoc(body string) string {
	var b strings.Builder
	b.WriteString("<<-EOT\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	b.WriteString("  EOT")
	return b.String()
}
