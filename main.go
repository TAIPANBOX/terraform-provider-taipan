// Command terraform-provider-taipan serves the taipan Terraform provider:
// governance as code for the TAIPANBOX agent-governance stack (TokenFuse
// Cloud spend budgets, Idryx/Qryx agent passports).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/TAIPANBOX/terraform-provider-taipan/internal/provider"
)

// version is set via -ldflags "-X main.version=..." at release build time
// (see the Makefile's VERSION/LDFLAGS); "dev" for local builds.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/TAIPANBOX/taipan",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
