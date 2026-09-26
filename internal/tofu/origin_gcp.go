package tofu

import (
	"fmt"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// gcpOrigin builds the backend of an external HTTP load balancer.
//
// Google's edge does not fetch from a name. A backend service points at a group,
// the group publishes a named port, and the backend service asks for that port by
// name — so the port drawn on the line has to reach three arguments at once, and
// a health check on the wrong one marks the instance unhealthy while every
// resource in the file looks correct.
//
// The group is a companion of the load balancer rather than of the instance,
// because it exists only because a line was drawn. An instance with no load
// balancer in front of it must not grow one.
type gcpOrigin struct{}

// The named port is referenced from two resources, so it is spelled once.
const gcpOriginPort = "origin"

func (gcpOrigin) wire(edge, from Endpoint, rules []model.Rule) wiring {
	port, reason := originPort(rules)
	if reason != "" {
		return wiring{reason: reason}
	}
	// A backend bucket fronts storage and has no backends to attach, so a line
	// from one to an instance describes something Google cannot build.
	if edge.Type.TofuType != "google_compute_backend_service" {
		return wiring{reason: fmt.Sprintf(
			"%s fronts a storage bucket and cannot reach %s — put an HTTPS load balancer in front of it instead",
			edge.Asset.Name, from.Asset.Name)}
	}
	instance := fmt.Sprintf("%s.%s", from.Type.TofuType, from.Asset.ID)
	return wiring{
		fixed: []catalog.Fixed{
			{Key: "port_name", Expr: quote(gcpOriginPort)},
			{Key: "health_checks", Expr: "[google_compute_health_check.{{asset}}_hc.id]"},
			{Block: "backend", Key: "group", Expr: "google_compute_instance_group.{{asset}}_origin.self_link"},
		},
		companions: []catalog.Companion{{
			TofuType: "google_compute_instance_group", Suffix: "origin",
			Fixed: []catalog.Fixed{
				{Key: "name", Expr: `"{{asset-dashed}}-origin"`},
				{Key: "instances", Expr: fmt.Sprintf("[%s.self_link]", instance)},
				// The group is zonal and has to sit where the instance does.
				{Key: "zone", Expr: instance + ".zone"},
				{Block: "named_port", Key: "name", Expr: quote(gcpOriginPort)},
				{Block: "named_port", Key: "port", Expr: port},
			},
		}, {
			TofuType: "google_compute_health_check", Suffix: "hc",
			Fixed: []catalog.Fixed{
				{Key: "name", Expr: `"{{asset-dashed}}-hc"`},
				// The same port the backend serves. A check against 80 while the
				// application listens on 8080 fails every probe, and the load balancer
				// returns 502 with nothing in the configuration looking wrong.
				{Block: "http_health_check", Key: "port", Expr: port},
			},
		}},
	}
}

// unwired is a backend service with nothing drawn behind it: no backends, and so
// no health check to require. That is valid configuration — it serves a 502 until
// an origin is connected, which is an honest description of an empty chart.
func (gcpOrigin) unwired(Endpoint) wiring { return wiring{} }
