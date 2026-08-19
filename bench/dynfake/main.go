// dynfake's plugin-process boilerplate — identical in shape to
// cmd/openbao-plugin-secrets-voidstar/main.go, wired to this
// package's local factory instead of backend.Factory.
package main

import (
	"os"

	"github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/api/v2"
	"github.com/openbao/openbao/sdk/v2/plugin"
)

func main() {
	apiClientMeta := &api.PluginAPIClientMeta{}
	flags := apiClientMeta.FlagSet()
	if err := flags.Parse(os.Args[1:]); err != nil {
		hclog.L().Error("failed to parse flags", "error", err)
		os.Exit(1)
	}

	tlsProviderFunc := api.VaultPluginTLSProvider(apiClientMeta.GetTLSConfig())

	if err := plugin.ServeMultiplex(&plugin.ServeOpts{
		BackendFactoryFunc: factory,
		TLSProviderFunc:    tlsProviderFunc,
	}); err != nil {
		hclog.L().Error("plugin shutting down", "error", err)
		os.Exit(1)
	}
}
