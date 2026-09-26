package tofu

import (
	"fmt"
	"strings"

	"infrachart/internal/model"
)

// AWS is the straightforward case: one security group per asset, and one rule
// resource per rule. A source can be another security group, which is the only
// representation that cannot go stale.
type awsFirewall struct{ ruleList }

func (f *awsFirewall) render(w *writer, acc model.Account) {
	assets, grouped := byAsset(f.rules)
	for _, asset := range assets {
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_security_group", asset), func() {
			w.arg("provider", "aws."+acc.ID)
			w.arg("name", quote(asset))
			w.arg("vpc_id", fmt.Sprintf("aws_vpc.%s.id", acc.ID))
		})
		for i, r := range grouped[asset] {
			f.renderRule(w, acc.ID, r, i)
		}
	}
}

func (f *awsFirewall) renderRule(w *writer, accountID string, r fwRule, index int) {
	resource := "aws_vpc_security_group_ingress_rule"
	if r.Dir == dirEgress {
		resource = "aws_vpc_security_group_egress_rule"
	}
	w.blank()
	w.line("# %s", r.Comment)
	w.block(fmt.Sprintf("resource %q %q", resource, ruleName(r, index)), func() {
		w.arg("provider", "aws."+accountID)
		w.arg("security_group_id", fmt.Sprintf("aws_security_group.%s.id", r.Asset))
		w.arg("ip_protocol", quote(lowerProto(r.Proto, "-1")))
		// TCP and UDP rules must carry a port range even when the rule covers all
		// ports: AWS rejects a tcp rule with no ports at apply time, and `validate`
		// does not catch it because the schema marks them optional.
		if r.Proto == "TCP" || r.Proto == "UDP" {
			lo, hi := r.Lo, r.Hi
			if r.AnyPort {
				lo, hi = 0, 65535
			}
			w.arg("from_port", fmt.Sprint(lo))
			w.arg("to_port", fmt.Sprint(hi))
		}
		switch r.Kind {
		case peerSecurityGroup:
			w.arg("referenced_security_group_id", fmt.Sprintf("aws_security_group.%s.id", r.Peer))
		case peerPrefixList:
			w.arg("prefix_list_id", fmt.Sprintf("data.aws_ec2_managed_prefix_list.%s.id", prefixListName(r.Peer)))
		default:
			w.arg("cidr_ipv4", interp(r.Peer))
		}
	})
}

// prefixListName turns a managed prefix list name into an HCL-safe data source
// name, e.g. com.amazonaws.global.cloudfront.origin-facing.
func prefixListName(list string) string {
	r := strings.NewReplacer(".", "_", "-", "_")
	return r.Replace(list)
}
