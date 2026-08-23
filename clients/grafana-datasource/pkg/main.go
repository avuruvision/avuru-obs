// Command avuru-obs-datasource is the backend half of the Avuru Obs Grafana
// data source.
//
// Grafana starts this as a subprocess and speaks gRPC to it. Everything the
// plugin knows about authentication lives here rather than in the browser: the
// API token is stored in Grafana's encrypted secure settings and decrypted only
// in this process.
package main

import (
	"os"

	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"

	"github.com/avuru/avuru-obs/clients/grafana-datasource/pkg/plugin"
)

func main() {
	if err := datasource.Manage("avuru-obs-datasource", plugin.NewDatasource, datasource.ManageOpts{}); err != nil {
		log.DefaultLogger.Error("plugin exited", "error", err.Error())
		os.Exit(1)
	}
}
