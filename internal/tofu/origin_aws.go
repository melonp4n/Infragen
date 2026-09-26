package tofu

import (
	"fmt"

	"infrachart/internal/catalog"
	"infrachart/internal/model"
)

// awsOrigin builds the origin block of a CloudFront distribution.
//
// It owns that block outright, including the case where nothing is drawn behind
// the distribution. The catalog used to pin domain_name to a variable defaulting
// to origin.example.com, so a chart could draw a CDN in front of an instance,
// generate an ingress rule for CloudFront's prefix list, and still fetch from
// nowhere. Two sources writing the same argument would emit it twice, so there
// is exactly one.
type awsOrigin struct{}

func (awsOrigin) wire(edge, from Endpoint, rules []model.Rule) wiring {
	port, reason := originPort(rules)
	if reason != "" {
		return wiring{reason: reason}
	}
	policy, _ := edge.Asset.Params[catalog.ParamOriginProtocol].(string)

	// A function URL is served over HTTPS on 443 and nothing else, so a chart
	// saying otherwise is refused rather than quietly overridden.
	lambda := from.Type.Origin.RequiresParam == catalog.ParamFunctionURL
	if lambda {
		if port != "443" {
			return wiring{reason: fmt.Sprintf(
				"a function URL is served over HTTPS on 443, so %s cannot be fetched on %s — set the port to 443",
				from.Asset.Name, port)}
		}
		if policy == "http-only" {
			return wiring{reason: fmt.Sprintf(
				"a function URL has no plaintext port, so %s cannot use an origin protocol of http-only",
				edge.Asset.Name)}
		}
	}

	// http_port and https_port are both required. The drawn port fills the one the
	// protocol policy actually uses; the other keeps a default nothing connects to.
	httpPort, httpsPort := "80", "443"
	if policy == "http-only" {
		httpPort = port
	} else {
		httpsPort = port
	}
	w := wiring{fixed: []catalog.Fixed{
		{Block: "origin", Key: "domain_name", Expr: originExpr(from)},
		{Block: "origin.custom_origin_config", Key: "http_port", Expr: httpPort},
		{Block: "origin.custom_origin_config", Key: "https_port", Expr: httpsPort},
		{Block: "origin.custom_origin_config", Key: "origin_protocol_policy",
			Expr: "{{param:" + catalog.ParamOriginProtocol + "}}"},
		{Block: "origin.custom_origin_config", Key: "origin_ssl_protocols", Expr: `["TLSv1.2"]`},
	}}
	if lambda {
		return lambdaOrigin(from, w)
	}
	return w
}

// unwired is the origin block for a distribution with nothing drawn behind it.
//
// The variable is declared here rather than in the catalog so that it exists only
// in the case that needs it: a chart that wires a real origin should not also
// declare a domain nothing reads.
func (awsOrigin) unwired(edge Endpoint) wiring {
	name := edge.Asset.ID + "_origin_domain"
	return wiring{
		fixed: []catalog.Fixed{
			{Block: "origin", Key: "domain_name", Expr: "var." + name},
			{Block: "origin.custom_origin_config", Key: "http_port", Expr: "80"},
			{Block: "origin.custom_origin_config", Key: "https_port", Expr: "443"},
			{Block: "origin.custom_origin_config", Key: "origin_protocol_policy",
				Expr: "{{param:" + catalog.ParamOriginProtocol + "}}"},
			{Block: "origin.custom_origin_config", Key: "origin_ssl_protocols", Expr: `["TLSv1.2"]`},
		},
		vars: []catalog.Variable{{
			Name:        name,
			Description: "Origin the distribution fetches from. Draw a line to an asset to wire this instead.",
			Default:     `"origin.example.com"`,
		}},
	}
}

// lambdaOrigin adds what CloudFront needs to call a function URL whose
// authorization type is AWS_IAM.
//
// Without the access control the distribution sends an unsigned request and the
// function rejects it, which reads as a broken deployment rather than as the
// secure default working. Defaulting the function URL to public instead is not an
// option: a security setting's default is the safe value.
func lambdaOrigin(from Endpoint, w wiring) wiring {
	w.fixed = append(w.fixed, catalog.Fixed{
		Block: "origin", Key: "origin_access_control_id",
		Expr: "aws_cloudfront_origin_access_control.{{asset}}_oac.id",
	})
	w.companions = append(w.companions,
		catalog.Companion{
			TofuType: "aws_cloudfront_origin_access_control", Suffix: "oac",
			Fixed: []catalog.Fixed{
				{Key: "name", Expr: `"{{asset-dashed}}-oac"`},
				{Key: "origin_access_control_origin_type", Expr: `"lambda"`},
				{Key: "signing_behavior", Expr: `"always"`},
				{Key: "signing_protocol", Expr: `"sigv4"`},
			},
		},
		catalog.Companion{
			TofuType: "aws_lambda_permission", Suffix: "origin_perm",
			Fixed: []catalog.Fixed{
				{Key: "action", Expr: `"lambda:InvokeFunctionUrl"`},
				{Key: "function_name", Expr: fmt.Sprintf("aws_lambda_function.%s.function_name", from.Asset.ID)},
				{Key: "principal", Expr: `"cloudfront.amazonaws.com"`},
				{Key: "function_url_auth_type", Expr: `"AWS_IAM"`},
				{Key: "source_arn", Expr: "aws_cloudfront_distribution.{{asset}}.arn"},
			},
		},
	)
	return w
}
