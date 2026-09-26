package tofu

import "fmt"

// scaffold emits the network each provider needs before anything can be attached
// to it. Minimal by design: a chart says nothing about subnetting.
func scaffold(w *writer, provider, accountID string) {
	local := accountID
	switch provider {
	case "aws":
		w.blank()
		w.block(fmt.Sprintf("data %q %q", "aws_availability_zones", local), func() {
			w.arg("provider", "aws."+local)
			w.arg("state", quote("available"))
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_vpc", local), func() {
			w.arg("provider", "aws."+local)
			w.arg("cidr_block", quote("10.0.0.0/16"))
		})
		// Two subnets in different zones: a DB subnet group requires two, and so
		// does an application load balancer.
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_subnet", local), func() {
			w.arg("provider", "aws."+local)
			w.arg("vpc_id", fmt.Sprintf("aws_vpc.%s.id", local))
			w.arg("cidr_block", quote("10.0.1.0/24"))
			w.arg("availability_zone", fmt.Sprintf("data.aws_availability_zones.%s.names[0]", local))
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_subnet", local+"_b"), func() {
			w.arg("provider", "aws."+local)
			w.arg("vpc_id", fmt.Sprintf("aws_vpc.%s.id", local))
			w.arg("cidr_block", quote("10.0.2.0/24"))
			w.arg("availability_zone", fmt.Sprintf("data.aws_availability_zones.%s.names[1]", local))
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_internet_gateway", local), func() {
			w.arg("provider", "aws."+local)
			w.arg("vpc_id", fmt.Sprintf("aws_vpc.%s.id", local))
		})
		// A gateway with nothing routed to it is a gateway that does nothing. Without
		// this table both subnets are private: an instance can hold a public IP and
		// allow port 22 and still be unreachable, because the reply has no way out.
		//
		// An inline route rather than a separate aws_route resource: one resource
		// instead of two, and the inline form is authoritative, so nothing can add a
		// route behind the chart's back.
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "aws_route_table", local), func() {
			w.arg("provider", "aws."+local)
			w.arg("vpc_id", fmt.Sprintf("aws_vpc.%s.id", local))
			w.block("route", func() {
				w.arg("cidr_block", quote("0.0.0.0/0"))
				w.arg("gateway_id", fmt.Sprintf("aws_internet_gateway.%s.id", local))
			})
		})
		// Both subnets, because an asset may land in either and a load balancer
		// spans both. A subnet left unassociated falls back to the VPC's main route
		// table, which has no gateway route — reachable or not by accident.
		for _, subnet := range []string{local, local + "_b"} {
			w.blank()
			w.block(fmt.Sprintf("resource %q %q", "aws_route_table_association", subnet), func() {
				w.arg("provider", "aws."+local)
				w.arg("subnet_id", fmt.Sprintf("aws_subnet.%s.id", subnet))
				w.arg("route_table_id", fmt.Sprintf("aws_route_table.%s.id", local))
			})
		}
	case "azure":
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_resource_group", local), func() {
			w.arg("provider", "azurerm."+local)
			w.arg("name", quote(local))
			w.arg("location", "var."+local+"_region")
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_virtual_network", local), func() {
			w.arg("provider", "azurerm."+local)
			w.arg("name", quote(local))
			w.arg("address_space", fmt.Sprintf("[%s]", quote("10.0.0.0/16")))
			w.arg("location", fmt.Sprintf("azurerm_resource_group.%s.location", local))
			w.arg("resource_group_name", fmt.Sprintf("azurerm_resource_group.%s.name", local))
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "azurerm_subnet", local), func() {
			w.arg("provider", "azurerm."+local)
			w.arg("name", quote(local))
			w.arg("resource_group_name", fmt.Sprintf("azurerm_resource_group.%s.name", local))
			w.arg("virtual_network_name", fmt.Sprintf("azurerm_virtual_network.%s.name", local))
			w.arg("address_prefixes", fmt.Sprintf("[%s]", quote("10.0.1.0/24")))
		})
	case "gcp":
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "google_compute_network", local), func() {
			w.arg("provider", "google."+local)
			w.arg("name", quote(dashed(local)))
			w.arg("auto_create_subnetworks", "false")
		})
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "google_compute_subnetwork", local), func() {
			w.arg("provider", "google."+local)
			w.arg("name", quote(dashed(local)))
			w.arg("ip_cidr_range", quote("10.0.1.0/24"))
			w.arg("region", "var."+local+"_region")
			w.arg("network", fmt.Sprintf("google_compute_network.%s.id", local))
		})
	case "digitalocean":
		w.blank()
		w.block(fmt.Sprintf("resource %q %q", "digitalocean_vpc", local), func() {
			w.arg("name", quote(dashed(local)))
			w.arg("region", "var."+local+"_region")
		})
	}
}
