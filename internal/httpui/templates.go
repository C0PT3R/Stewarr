package httpui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"connarr/internal/product"
)

// UI source and its pinned browser dependencies ship inside the Connarr
// binary. Running Connarr never depends on a CDN or a JavaScript build service.
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
	return template.HTML(`<link rel="stylesheet" href="/assets/app.css">
<script defer src="/assets/vendor/htmx-2.0.10.min.js"></script>
<script defer src="/assets/vendor/stimulus-3.2.2.umd.js"></script>
<script defer src="/assets/app.js"></script>`)
}

func appChrome(active string) string {
	links := []struct{ Key, Label, URL string }{
		{"home", "Home", "/"},
		{"library", "Library", "/library"},
		{"torrents", "Torrents", "/torrents"},
		{"unmanaged", "Unmanaged", "/downloads/unmanaged"},
		{"tasks", "Tasks", "/tasks"},
		{"history", "History", "/history"},
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
	chrome.WriteString(`</nav><a id="operation-indicator" class="operation-indicator" href="/history" hidden></a></header>
<aside id="updates-available" class="updates-available" hidden data-controller="updates"><button type="button" data-action="updates#apply">Updates available</button></aside>
<div id="persistent-notices" class="persistent-notices" aria-live="polite"></div>
<div id="removal-modal" class="modal-root"></div>
<div id="ui-announcer" class="visually-hidden" aria-live="polite" aria-atomic="true"></div>`)
	return chrome.String()
}
