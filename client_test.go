package client_test

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Elagoht/collage/pkg/collage"
	"github.com/Elagoht/collage/pkg/collagetest"

	client "github.com/Elagoht/collage-client"
)

// site is an application with the plugin and a layout calling clientjs as call
// does: {{clientjs}}, {{clientjs "abc"}}.
func site(t *testing.T, call string, options ...collagetest.SiteOption) *collagetest.Site {
	t.Helper()
	return collagetest.NewSite(t, append([]collagetest.SiteOption{
		collagetest.WithPlugins(client.New()),
		collagetest.WithTemplates(map[string]string{
			"layout.html": `<!doctype html><html><head>` + call + `</head><body>{{slot "content"}}</body></html>`,
		}),
	}, options...)...)
}

var scriptTag = regexp.MustCompile(`<script src="(/_client/client\.([0-9a-f]{16})\.js)"([^>]*)></script>`)

// scriptOf returns the script element the home page carries, its src and the
// attributes after src.
func scriptOf(t *testing.T, s *collagetest.Site) (src, rest string) {
	t.Helper()
	body := s.Get("/").WantStatus(http.StatusOK).Body
	m := scriptTag.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no script element at a hashed URL in:\n%s", body)
	}
	if n := strings.Count(body, "<script"); n != 1 {
		t.Errorf("%d script elements, want 1:\n%s", n, body)
	}
	return m[1], m[3]
}

func TestVersion(t *testing.T) {
	p := client.New()
	if p.Name() != "elagoht/client" || p.Name() != client.Name {
		t.Errorf("Name() = %q", p.Name())
	}
	if p.Version() != "0.1.1" {
		t.Errorf("Version() = %q, want 0.1.0", p.Version())
	}
}

func TestTag(t *testing.T) {
	_, rest := scriptOf(t, site(t, `{{clientjs}}`))
	if rest != " defer" {
		t.Errorf("attributes after src = %q, want only defer, no nonce", rest)
	}
}

func TestTag_Nonce(t *testing.T) {
	_, rest := scriptOf(t, site(t, `{{clientjs "n0nce&"}}`))
	if rest != ` nonce="n0nce&amp;" defer` {
		t.Errorf("attributes after src = %q, want the escaped nonce and defer", rest)
	}
}

func TestTag_EmptyNonce(t *testing.T) {
	_, rest := scriptOf(t, site(t, `{{clientjs ""}}`))
	if rest != " defer" {
		t.Errorf("attributes after src = %q, want only defer", rest)
	}
}

func TestTag_TooManyArguments(t *testing.T) {
	s := site(t, `{{clientjs "a" "b"}}`)
	res := s.Get("/")
	if res.Status == http.StatusOK && strings.Contains(res.Body, "<script") {
		t.Errorf("two arguments rendered a script: %d\n%s", res.Status, res.Body)
	}
	if !s.Logs.Has(slog.LevelWarn, "clientjs takes at most one argument, the nonce; got 2") {
		t.Errorf("no error saying clientjs takes at most one argument; logs:\n%s", s.Logs)
	}
}

func TestScript_Served(t *testing.T) {
	s := site(t, `{{clientjs}}`)
	src, _ := scriptOf(t, s)
	res := s.Get(src).WantStatus(http.StatusOK)
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want a year and immutable", cc)
	}
	for _, want := range []string{"window.collage", "Idiomorph", "Zero-Clause BSD", "idiomorph 0.8.0", `"Collage-Fetch": "1"`} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("the script does not contain %q", want)
		}
	}
	if !strings.HasPrefix(res.Body, "(function () {") || !strings.HasSuffix(res.Body, "})();\n") {
		t.Error("the script is not one function: idiomorph's global would leak")
	}
	// The hash is the served bytes' own.
	sum := sha256.Sum256([]byte(res.Body))
	hash := strings.TrimSuffix(strings.TrimPrefix(src, "/_client/client."), ".js")
	if !strings.HasPrefix(hex.EncodeToString(sum[:]), hash) {
		t.Errorf("the URL's hash %s is not the script's SHA-256 %x", hash, sum)
	}
}

func TestScript_UnhashedNameRevalidates(t *testing.T) {
	res := site(t, `{{clientjs}}`).Get(client.ScriptPath).WantStatus(http.StatusOK)
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestScript_UnknownHash404s(t *testing.T) {
	s := site(t, `{{clientjs}}`)
	src, _ := scriptOf(t, s)
	// Well-formed — sixteen hex digits where the hash goes — and not the hash.
	wrong := "/_client/client.0123456789abcdef.js"
	if wrong == src {
		wrong = "/_client/client.fedcba9876543210.js"
	}
	s.Get(wrong).WantStatus(http.StatusNotFound)
	s.Get("/_client/other.js").WantStatus(http.StatusNotFound)
}

// The plugin's prefix is its own: an application page next to it is untouched.
func TestPrefix_DoesNotShadow(t *testing.T) {
	about := collage.NewPage("about").
		WithLayouts(collage.NewFragment("layout", "layout.html").Build()).
		WithContent(collage.NewFragment("about", "about.html").Build()).
		WithPath("en", "/client").
		Build()
	s := site(t, `{{clientjs}}`,
		collagetest.WithTemplates(map[string]string{"about.html": `<p>about</p>`}),
		collagetest.WithRegister(about))
	if res := s.Get("/client").WantStatus(http.StatusOK); !strings.Contains(res.Body, "<p>about</p>") {
		t.Errorf("/client = %s", res.Body)
	}
}
