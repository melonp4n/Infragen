# InfraGen

> [!CAUTION] 
> This is a prototype application and is mostly vibe-coded and filled with bugs. 

## About

Infragen is a web-application that allows users to build Cloud infrastructure using drag-and-drop nodes and flow chart connections representing traffic ingress/egress rules. The output of an infrastructure map can then be used to generate Terraform/OpenTofu for deployment into live environments. This was originally developed as a visual aid for red-team infrastructure, and will be catered towards that.

## Design Considerations

This application is written primarily in Go, using templ as a front-end templating engine. Nodes use a generic template, and the content is derived from the resource models listed in /catalog. This has been developed around modularity, allowing additional resources to be supported with minimal refactoring. 

> [!NOTE]
> The FlowChart -> Tofu logic used to be the exception, with per-provider case logic spread through
> one file. Firewall generation is now one file per provider behind a shared interface, and what a
> firewall attaches to is catalog data rather than a switch. Network scaffolding is still hardcoded
> per provider in `internal/tofu/scaffold.go`.

The use of models also allows easy import/export to JSON files, with templ handling input sanitisation.

## How to

```
go tool templ generate && go run . -seed
```

`go tool templ generate` will generate the go source from the .templ template files.
`-seed` is used to provide a generic base infrastructure, but can be omitted to start with a blank canvas.

The application defaults to localhost:8080

## Screenshot

![Flow-chart infrastructure map](map.png)

![Sample Terraform/OpenTofu output](tf_out.png)

## Feature Roadmap

- [x] Add configuration options to support either init-scripts or integrations with Ansible
- [ ] Add DNS support, integrating with popular Registrars (this will include automated checks for health and reputation)
- [ ] Add the option to apply the OpenTofu plan and detect configuration drift. This will help with quick teardown/restoration of burnt assets
- [x] Clean up /internal/tofu/firewall.go to make it easier to add other providers — one file per provider, shared port/protocol helpers, and attachment declared in the catalog
- [ ] Refactor the slop out

