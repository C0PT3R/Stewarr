// Command buildassets bundles the TypeScript sources under
// internal/httpui/static/src into the single internal/httpui/static/app.js
// file the httpui package embeds and serves. It is invoked by the Makefile
// (build/run/deploy) and the Dockerfile before `go build`/`go test`, since
// //go:embed requires app.js to already exist on disk.
package main

import (
	"fmt"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{"internal/httpui/static/src/app.ts"},
		Outfile:     "internal/httpui/static/app.js",
		Bundle:      true,
		Write:       true,
		Format:      api.FormatIIFE,
		Target:      api.ES2020,
		Platform:    api.PlatformBrowser,
		LogLevel:    api.LogLevelInfo,
	})
	for _, w := range result.Warnings {
		fmt.Fprintln(os.Stderr, "[buildassets] warning:", w.Text)
	}
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintln(os.Stderr, "[buildassets] error:", e.Text)
		}
		os.Exit(1)
	}
}
