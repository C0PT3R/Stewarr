// Command buildassets bundles the TypeScript sources under
// internal/httpui/static/src, together with the pinned vendor scripts
// (htmx, Stimulus), into the single internal/httpui/static/app.js file the
// httpui package embeds and serves. It is invoked by the Makefile
// (build/run/deploy) and the Dockerfile before `go build`/`go test`, since
// //go:embed requires app.js to already exist on disk.
//
// Every page load competes for the browser's ~6-connections-per-origin
// HTTP/1.1 limit against its own assets (this app has no TLS, so no
// HTTP/2 multiplexing). Serving htmx, Stimulus, and the app bundle as one
// file instead of three separate <script> tags frees up two of those
// slots for the things that actually need them, like the live-updates
// EventSource.
package main

import (
	"fmt"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	vendorBanner, err := vendorScripts(
		"internal/httpui/static/vendor/htmx-2.0.10.min.js",
		"internal/httpui/static/vendor/stimulus-3.2.2.umd.js",
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[buildassets] error:", err)
		os.Exit(1)
	}
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{"internal/httpui/static/src/app.ts"},
		Outfile:     "internal/httpui/static/app.js",
		Bundle:      true,
		Write:       true,
		Format:      api.FormatIIFE,
		Target:      api.ES2020,
		Platform:    api.PlatformBrowser,
		LogLevel:    api.LogLevelInfo,
		Banner:      map[string]string{"js": vendorBanner},
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

func vendorScripts(paths ...string) (string, error) {
	var combined string
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read vendor script %s: %w", path, err)
		}
		combined += string(content) + "\n;\n"
	}
	return combined, nil
}
