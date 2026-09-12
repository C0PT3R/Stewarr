package httpui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"stewarr/internal/product"
)

// UI source and its pinned browser dependencies ship inside the Stewarr
// binary. Running Stewarr never depends on a CDN or a JavaScript build service.
//
//go:embed templates/*.html static/*
var uiFiles embed.FS

func parseUITemplate(name string, functions template.FuncMap) (*template.Template, error) {
	content, err := uiFiles.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("read UI template %s: %w", name, err)
	}
	parsed, err := template.New(name).Funcs(functions).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse UI template %s: %w", name, err)
	}
	return parsed, nil
}

func uiStaticHandler() (http.Handler, error) {
	staticFiles, err := fs.Sub(uiFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded UI assets: %w", err)
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(staticFiles))), nil
}

func appHead() template.HTML {
	// htmx and Stimulus are bundled into app.js by tools/buildassets rather
	// than served as separate <script> tags — every page load already
	// competes for the browser's ~6-connections-per-origin limit (no TLS,
	// no HTTP/2 multiplexing here), so one fewer script request matters.
	return template.HTML(`<link rel="stylesheet" href="/assets/app.css">
<script defer src="/assets/app.js"></script>`)
}

func appChrome(active string) string {
	links := []struct{ Key, Label, URL string }{
		{"home", "Home", "/"},
		{"library", "Library", "/library"},
		{"torrents", "Torrents", "/torrents"},
		{"unmanaged", "Unmanaged", "/unmanaged"},
		{"tasks", "Tasks", "/tasks"},
		{"history", "History", "/history"},
		{"settings", "Settings", "/settings"},
	}
	var chrome strings.Builder
	chrome.WriteString(`<header class="appbar"><a class="appbrand" href="/">` + product.Name + `</a><nav class="appnav" aria-label="Primary">`)
	for _, link := range links {
		classAttribute := ""
		ariaCurrent := ""
		if link.Key == active {
			classAttribute = ` class="active"`
			ariaCurrent = ` aria-current="page"`
		}
		chrome.WriteString(`<a` + classAttribute + ariaCurrent + ` href="` + link.URL + `">` + link.Label + `</a>`)
	}
	chrome.WriteString(`</nav><a id="operation-indicator" class="operation-indicator" href="/tasks" hidden></a><form class="signout-form" method="post" action="/logout"><button type="submit" class="signout-button">Sign out</button></form></header>
<aside id="updates-available" class="updates-available" hidden data-controller="updates"><button type="button" data-action="updates#apply">Updates available</button></aside>
<div id="persistent-notices" class="persistent-notices" aria-live="polite"></div>
<div id="modal-root" class="modal-root"></div>
<div id="ui-announcer" class="visually-hidden" aria-live="polite" aria-atomic="true"></div>`)
	return chrome.String()
}
