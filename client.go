// Package client is a collage plugin that serves a tiny client script: element
// getter shorthands, and a fragment fetched from its own URL put in place by
// replacing or morphing.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{client.New()},
//	})
//
// The master layout adds the script with one call, and passes the CSP nonce when
// collage-secure sets one:
//
//	<head>{{clientjs}}</head>
//	<head>{{clientjs cspNonce}}</head>
//
// The page then has window.collage:
//
//	collage.$(selector, root?)    // querySelector
//	collage.$$(selector, root?)   // querySelectorAll, as an Array
//	collage.replace(target, url?) // fetch a fragment, swap it in with outerHTML
//	collage.morph(target, url?)   // fetch a fragment, patch the DOM in place
//
// target is a selector or an Element, and url defaults to the element's data-src,
// where a page opened with WithFragmentPath puts {{fragmentURL}}. Both resolve to
// the element now in the page, after a bubbling collage:swap event on it, and
// reject, leaving the page untouched, when the fragment cannot be had.
//
// The script is served under /_client/ at a content-addressed name, so it is
// cached for a year and a new release is a new URL. The morphing is idiomorph
// 0.8.0 (0BSD), bundled into the same file.
package client

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
	"testing/fstest"

	"github.com/Elagoht/collage/pkg/collage"
)

// Name is the plugin's name.
const Name = "elagoht/client"

// Prefix is where the script is served. It is the plugin's own, so it shadows
// none of the application's routes.
const Prefix = "/_client/"

// ScriptPath is the script's own name; {{clientjs}} links its content-addressed
// one.
const ScriptPath = Prefix + "client.js"

//go:embed idiomorph.js
var idiomorphJS string

//go:embed client.js
var clientJS string

// script is the file served at ScriptPath: idiomorph and the client, inside one
// function, so the only global they leave is window.collage.
var script = []byte("(function () {\n" + idiomorphJS + "\n" + clientJS + "})();\n")

// Plugin serves the script and adds {{clientjs}}.
type Plugin struct{}

// New returns the plugin. There is nothing to configure.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string    { return Name }
func (p *Plugin) Version() string { return "0.1.0" }

// Configure adds {{clientjs}}. A render function, because the script's URL
// carries its content hash, which the render's own asset resolver mints.
func (p *Plugin) Configure(_ context.Context, host collage.ConfigHost) error {
	return host.AddRenderFunc("clientjs", func(rc *collage.RenderContext) any { // any: html/template.FuncMap's own value type
		return func(nonce ...string) (template.HTML, error) { return tag(rc, nonce...) }
	})
}

// Init serves the script.
func (p *Plugin) Init(_ context.Context, host collage.Host) error {
	// fstest.MapFS is the standard library's in-memory file system: a mount needs
	// files that seek and a directory a static build can walk, and it has both.
	// The name without its hash is served too, revalidated each time.
	if err := host.Mount(Prefix, fstest.MapFS{"client.js": {Data: script}}, collage.WithCacheControl("no-cache")); err != nil {
		return fmt.Errorf("client: serve the script: %w", err)
	}
	return nil
}

// Shutdown releases nothing.
func (p *Plugin) Shutdown(context.Context) error { return nil }

// tag is {{clientjs}}: the deferred script element at the content-addressed URL,
// with the nonce when one is given. The nonce is an argument, as in
// {{clientjs cspNonce}}, because collage-secure renders a placeholder that only it
// fills in per response; there is nothing in the render to read it from.
func tag(rc *collage.RenderContext, nonce ...string) (template.HTML, error) {
	if len(nonce) > 1 {
		return "", fmt.Errorf("client: clientjs takes at most one argument, the nonce; got %d", len(nonce))
	}
	src, err := rc.Asset(ScriptPath)
	if err != nil {
		return "", fmt.Errorf("client: %w", err)
	}
	var b strings.Builder
	b.WriteString(`<script src="` + template.HTMLEscapeString(src) + `"`)
	if len(nonce) == 1 && nonce[0] != "" {
		b.WriteString(` nonce="` + template.HTMLEscapeString(nonce[0]) + `"`)
	}
	b.WriteString(` defer></script>`)
	return template.HTML(b.String()), nil // assembled here from a constant layout and escaped values
}
