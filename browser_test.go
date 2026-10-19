//go:build unix

package client_test

// The browser test: the served script in a real headless Chrome, against a real
// collage application. The page runs every check on DOMContentLoaded and POSTs
// what it observed to a test-only action; the Go side asserts on that. Chrome is
// found at $CHROME (and only there, when it is set), else in the usual places;
// without it the test skips.

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Elagoht/collage/pkg/collage"

	client "github.com/Elagoht/collage-client"
)

// browserTimeout bounds the Chrome run, from launch to the result POST.
const browserTimeout = 20 * time.Second

// browserCSP admits only scripts carrying the nonce, so the client loading at
// all shows {{clientjs}} put the nonce on it.
const browserCSP = "script-src 'nonce-abc'"

const browserLayout = `<!doctype html><html><head><title>t</title>
<script>window.unnonced = true;</script>
{{clientjs "abc"}}{{hoist "head"}}</head><body>{{slot "content"}}</body></html>`

// The box is the fragment opened at /box: a counter that changes on every
// render, and two fields for the reader to type into.
const browserBox = `<form id="box" data-src="{{fragmentURL "home" "box"}}" onsubmit="return false">
<span id="count">{{.N}}</span>
<input id="name" name="name">
<input id="other" name="other">
</form>`

const browserContent = `{{slot "box"}}
<div id="nosrc">no data-src</div>
<div id="crosssrc">another origin's data-src, set below</div>
<ul id="list"><li id="a">a</li><li id="b"><textarea id="bin"></textarea></li></ul>
<div id="plain">old</div>
<div id="collage">an element the browser names window.collage</div>
<script nonce="abc">
document.addEventListener("DOMContentLoaded", async function () {
  var checks = {}, notes = {}, events = [];
  function check(name, ok) { checks[name] = !!ok; }
  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
  document.addEventListener("collage:swap", function (e) {
    events.push({ target: e.target, detail: e.detail, bubbles: e.bubbles });
  });
  async function rejects(name, promise, want) {
    var before = document.body.innerHTML, n = events.length;
    try {
      await promise;
      check(name, false);
      notes[name] = "resolved";
    } catch (e) {
      notes[name] = String(e && e.message);
      check(name, e instanceof Error && notes[name].indexOf(want) >= 0 &&
        document.body.innerHTML === before && events.length === n);
    }
  }
  try {
    var c = window.collage;
    check("loaded", !!c);
    check("cspActive", window.unnonced !== true);
    check("onlyGlobal", typeof window.Idiomorph === "undefined");

    // $ and $$.
    var box = document.getElementById("box");
    check("$", c.$("#box") === box && c.$("#nope") === null);
    check("$Root", c.$("input", box) === document.getElementById("name"));
    var all = c.$$("input"), inBox = c.$$("input", box);
    check("$$Array", Array.isArray(all) && Array.isArray(inBox));
    check("$$", all.length === 2 && inBox.length === 2 && inBox[1].id === "other");
    check("$$Empty", Array.isArray(c.$$(".nope")) && c.$$(".nope").length === 0);

    // morph keeps the node, what is typed into the focused field, the caret.
    var count0 = c.$("#count").textContent;
    var input = c.$("#name"), other = c.$("#other");
    other.value = "typed elsewhere";
    input.focus();
    input.value = "milk";
    input.setSelectionRange(1, 3);
    var got = await c.morph("#box");
    var ev = events[events.length - 1];
    check("morphChanged", c.$("#count").textContent !== count0);
    check("morphSameNode", got === box && c.$("#box") === box && c.$("#name") === input);
    check("morphValue", input.value === "milk");
    check("morphFocus", document.activeElement === input);
    check("morphCaret", input.selectionStart === 1 && input.selectionEnd === 3);
    check("morphEvent", events.length === 1 && ev.target === got && ev.bubbles &&
      ev.detail.mode === "morph" && ev.detail.url === box.getAttribute("data-src"));
    notes.otherAfterMorph = other.value;

    // replace puts a new element in place of the old, by Element and by selector.
    var count1 = c.$("#count").textContent;
    var replaced = await c.replace(box);
    ev = events[events.length - 1];
    check("replaceNew", replaced !== box && !box.isConnected && replaced.isConnected && c.$("#box") === replaced);
    check("replaceChanged", c.$("#count").textContent !== count1);
    check("replaceEvent", events.length === 2 && ev.target === replaced && ev.bubbles &&
      ev.detail.mode === "replace" && ev.detail.url === "/box");
    var again = await c.replace("#box", "/box?via=url");
    ev = events[events.length - 1];
    check("replaceURL", again.isConnected && events.length === 3 && ev.detail.url === "/box?via=url");

    // Failures leave the page as it was, fire nothing, and say why. The 500 is
    // one element, so only its status can refuse it.
    await rejects("fail500Replace", c.replace("#box", "/_test/500"), "answered 500");
    await rejects("fail500Morph", c.morph("#box", "/_test/500"), "answered 500");
    await rejects("fail404", c.morph("#box", "/_test/missing"), "answered 404");
    await rejects("failTwo", c.replace("#box", "/_test/two"), "not a single element");
    await rejects("failText", c.morph("#box", "/_test/text"), "not a single element");
    await rejects("failDocument", c.replace("#box", "/_test/document"), "not a single element");
    await rejects("failNoTarget", c.replace("#nope"), "no element #nope");
    await rejects("failNoSrc", c.morph("#nosrc"), "no data-src");
    await rejects("failNetwork", c.replace("#box", "/_hangup"), "replace /_hangup: ");
    // Markup from another origin is never inserted: not asked for, and not
    // reached through a redirect. The other origin allows CORS, so only the
    // origin rule refuses it.
    var other = new URLSearchParams(location.search).get("other");
    c.$("#crosssrc").setAttribute("data-src", other + "/evil");
    await rejects("failCrossOrigin", c.replace("#box", other + "/evil"), "not this site's origin");
    await rejects("failCrossOriginSrc", c.morph("#crosssrc"), "not this site's origin");
    await rejects("failRedirect", c.morph("#box", "/_test/redirect"), "redirected off this site's origin");
    // A document a <template> would strip down to one element.
    await rejects("failCommentDocument", c.replace("#box", "/_test/comment-document"), "not a single element");
    await rejects("failBodyDocument", c.morph("#box", "/_test/body-document"), "not a single element");
    check("notPwned", window.pwned !== true && !c.$("img"));
    check("stillThere", c.$("#box") === again && again.isConnected);

    // Two calls on one element: the newer wins, the older rejects as superseded.
    async function race(mode) {
      var n = events.length;
      var slow = c[mode]("#box", "/_test/slow?ms=600&label=slow-" + mode);
      await sleep(50);
      var fast = c[mode]("#box", "/_test/slow?ms=0&label=fast-" + mode);
      var r = await Promise.allSettled([slow, fast]);
      await sleep(700);
      notes["race" + mode] = r[0].status + ": " + String(r[0].reason && r[0].reason.message) +
        " | " + r[1].status + " | " + c.$("#count").textContent;
      return r[0].status === "rejected" && r[0].reason instanceof Error &&
        r[0].reason.message.indexOf("superseded") >= 0 &&
        r[1].status === "fulfilled" && r[1].value === c.$("#box") &&
        c.$("#count").textContent === "fast-" + mode && events.length === n + 1;
    }
    check("raceMorph", await race("morph"));
    check("raceReplace", await race("replace"));

    // put: the content in hand, the page changed when it returns.
    function throws(name, fn, want) {
      var before = document.body.innerHTML, n = events.length;
      try {
        fn();
        check(name, false);
        notes[name] = "returned";
      } catch (e) {
        notes[name] = String(e && e.message);
        check(name, e instanceof Error && notes[name].indexOf(want) >= 0 &&
          document.body.innerHTML === before && events.length === n);
      }
    }
    var list = c.$("#list"), bin = c.$("#bin"), liA = c.$("#a");
    bin.focus();
    bin.value = "eggs";
    bin.setSelectionRange(1, 2);
    var n0 = events.length;
    var put = c.put("#list", '<li id="z">z</li><li id="a">a!</li><li id="b"><textarea id="bin"></textarea></li>', { morph: true, inner: true });
    ev = events[events.length - 1];
    check("putInnerMorph", put === list && c.$("#list") === list && list.firstElementChild.id === "z" &&
      c.$("#a") === liA && liA.textContent === "a!" && list.children.length === 3);
    check("putInnerMorphKeeps", c.$("#bin") === bin && bin.value === "eggs" &&
      document.activeElement === bin && bin.selectionStart === 1 && bin.selectionEnd === 2);
    check("putInnerMorphEvent", events.length === n0 + 1 && ev.target === list && ev.bubbles &&
      ev.detail.mode === "morph" && ev.detail.inner === true && ev.detail.url === null);

    var plain = c.$("#plain");
    var filled = c.put(plain, 'one <b>two</b><script nonce="abc">window.putRan = true;</script>', { inner: true });
    ev = events[events.length - 1];
    check("putInnerReplace", filled === plain && plain.firstChild.data === "one " && c.$("b", plain).textContent === "two");
    check("putInnerReplaceEvent", ev.target === plain && ev.detail.mode === "replace" && ev.detail.inner === true);
    var frag = document.createDocumentFragment();
    frag.append("text ", document.createElement("i"));
    c.put(plain, frag, { inner: true });
    check("putInnerFragment", plain.childNodes.length === 2 && plain.lastChild.tagName === "I");
    check("putNoScript", window.putRan !== true);

    var outer = c.put("#plain", '<div id="plain">new</div>');
    ev = events[events.length - 1];
    check("putOuter", outer !== plain && !plain.isConnected && c.$("#plain") === outer && outer.textContent === "new");
    check("putOuterEvent", ev.target === outer && ev.detail.mode === "replace" && ev.detail.inner === false);
    var morphed = c.put(outer, '<div id="plain" class="m">newer</div>', { morph: true });
    check("putOuterMorph", morphed === outer && outer.className === "m" && outer.textContent === "newer");

    throws("putTwo", function () { c.put("#plain", "<p>a</p><p>b</p>"); }, "put: the answer is not a single element");
    throws("putText", function () { c.put("#plain", "just text", { morph: true }); }, "not a single element");
    throws("putDocument", function () { c.put("#plain", "<html><body><p>x</p></body></html>"); }, "not a single element");
    throws("putNoElement", function () { c.put("#nope", "<p></p>"); }, "put: no element #nope");
    throws("putDetached", function () { c.put(document.createElement("div"), "x", { inner: true }); }, "not in the page");
    throws("putNotContent", function () { c.put("#plain", 42, { inner: true }); }, "not markup or a node");

    // put aborts a fetch in flight on the element: the late answer never lands.
    var late = c.morph("#box", "/_test/slow?ms=400&label=late");
    await sleep(50);
    c.put("#box", '<form id="box"><span id="count">put</span></form>', { morph: true });
    var lateResult = await Promise.allSettled([late]);
    await sleep(450);
    check("putSupersedes", lateResult[0].status === "rejected" &&
      lateResult[0].reason.message.indexOf("superseded") >= 0 && c.$("#count").textContent === "put");

    // A second copy of the script keeps the first.
    var second = document.createElement("script");
    second.nonce = "abc";
    second.src = document.querySelector('script[src^="/_client/"]').src;
    await new Promise(function (r) { second.onload = second.onerror = r; document.head.append(second); });
    check("loadedTwice", window.collage === c);

    // An element taken out of the page while its fragment is fetched stays out.
    var leaving = c.$("#box");
    var pending = c.replace(leaving, "/_test/slow?ms=300&label=late");
    leaving.remove();
    var snapshot = document.body.innerHTML, before = events.length;
    try {
      await pending;
      check("failLeft", false);
    } catch (e) {
      notes.failLeft = String(e && e.message);
      check("failLeft", e instanceof Error && notes.failLeft.indexOf("left the page") >= 0 &&
        document.body.innerHTML === snapshot && events.length === before && !leaving.isConnected);
    }
  } catch (e) {
    notes.error = String(e && e.stack || e);
  }
  await fetch("/_result", { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ checks: checks, notes: notes }) });
});
</script>`

// observed is what the page reports.
type observed struct {
	Checks map[string]bool   `json:"checks"`
	Notes  map[string]string `json:"notes"`
}

// boxView is the box fragment's data.
type boxView struct{ N int64 }

// otherOrigin starts a second server, another origin to the site, that answers
// anything with markup that would run a script, and allows every origin to read
// it, so only the client's origin rule keeps it out of the page.
func otherOrigin(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Collage-Fetch")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<form id="box"><img src="x" onerror="window.pwned = true"></form>`)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

// browserSite starts the application on a free loopback port, behind browserCSP,
// and returns its base URL, the channel the page's result arrives on, and the
// count of requests to /box that carried Collage-Fetch: 1 and of those that did
// not. other is another origin's URL, which /_test/redirect sends to.
func browserSite(t *testing.T, other string) (string, <-chan []byte, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	a, err := collage.New(&collage.Config{
		Server: collage.ServerConfig{Host: "127.0.0.1", Port: port},
		Template: collage.TemplateConfig{FS: fstest.MapFS{
			"t/layout.html":  {Data: []byte(browserLayout)},
			"t/content.html": {Data: []byte(browserContent)},
			"t/box.html":     {Data: []byte(browserBox)},
		}, Root: "t"},
		Locale:  collage.LocaleConfig{Default: "en", Supported: []string{"en"}},
		Plugins: []collage.Plugin{client.New()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var renders, fetched, unfetched atomic.Int64
	box := collage.NewFragment("box", "box.html").
		WithData(collage.Load(func(_ context.Context, rc *collage.RenderContext) (boxView, error) {
			if rc.Request != nil && rc.Request.URL.Path == "/box" {
				if rc.Request.Header.Get("Collage-Fetch") == "1" {
					fetched.Add(1)
				} else {
					unfetched.Add(1)
				}
			}
			return boxView{N: renders.Add(1)}, nil
		})).Build()
	layout := collage.NewFragment("layout", "layout.html").WithSlot("content", collage.SlotRequired, collage.SlotSingle).Build()
	content := collage.NewFragment("content", "content.html").WithSlotFragment("box", box).Build()
	page := collage.NewPage("home").WithLayouts(layout).WithContent(content).
		WithPath("en", "/").WithFragmentPath("en", "/box", box).Build()
	results := make(chan []byte, 1)
	result := collage.NewAction("test-result").WithPath("en", "/_result").WithMethods(http.MethodPost).WithoutCSRF().
		WithHandler(func(_ context.Context, rc *collage.RenderContext) (*collage.ActionResult, error) {
			b, err := io.ReadAll(io.LimitReader(rc.Request.Body, 1<<20))
			if err != nil {
				return nil, err
			}
			select {
			case results <- b:
			default:
			}
			return collage.Status(http.StatusNoContent), nil
		}).Build()
	if err := a.Register(page, result); err != nil {
		t.Fatal(err)
	}
	answers := map[string]struct {
		status      int
		contentType string
		body        string
	}{
		"/_test/500":      {http.StatusInternalServerError, "text/html; charset=utf-8", `<form id="box">it broke</form>`},
		"/_test/two":      {http.StatusOK, "text/html; charset=utf-8", `<p id="box">one</p><p>two</p>`},
		"/_test/text":     {http.StatusOK, "text/plain; charset=utf-8", "just text"},
		"/_test/document": {http.StatusOK, "text/html; charset=utf-8", `<!doctype html><html><body><form id="box"></form></body></html>`},
		// What a <template> strips to one element: html, head and body dropped.
		"/_test/comment-document": {http.StatusOK, "text/html; charset=utf-8", `<!-- an error page --><html><head></head><body><form id="box">error</form></body></html>`},
		"/_test/body-document":    {http.StatusOK, "text/html; charset=utf-8", `<BODY class="error"><form id="box">error</form></body>`},
	}
	if err := a.Handle("/_test/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_test/redirect":
			http.Redirect(w, r, other+"/evil", http.StatusFound)
			return
		case "/_test/slow":
			// The box after a delay, labelled, so two calls racing on it can be
			// told apart.
			ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<form id="box" data-src="/box"><span id="count">`+
				html.EscapeString(r.URL.Query().Get("label"))+`</span><input id="name" name="name"><input id="other" name="other"></form>`)
			return
		}
		ans, ok := answers[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ans.contentType)
		w.WriteHeader(ans.status)
		_, _ = io.WriteString(w, ans.body)
	})); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	inner := a.Handler()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_hangup" {
			// A same-origin network failure: the connection closes unanswered.
			if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Security-Policy", browserCSP)
		inner.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(ctx)
	})
	return "http://127.0.0.1:" + strconv.Itoa(port), results, &fetched, &unfetched
}

// findChrome is $CHROME when it is set (an unusable value means no Chrome, so
// CHROME=/nonexistent skips), else the first browser found in the usual places.
func findChrome() string {
	if c, ok := os.LookupEnv("CHROME"); ok {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
		return ""
	}
	for _, c := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"google-chrome",
		"chromium",
	} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// browserChrome is the Chrome to run, or why the browser test skips. On CI
// (CI=true) it runs only when CHROME is set explicitly.
func browserChrome() (path, skipReason string) {
	if _, set := os.LookupEnv("CHROME"); !set && os.Getenv("CI") == "true" {
		return "", "CI: set CHROME to run the browser test"
	}
	if c := findChrome(); c != "" {
		return c, ""
	}
	return "", "no Chrome: set CHROME or install Google Chrome to run the browser test"
}

// runChrome opens pageURL in a fresh headless Chrome profile and returns the
// result the page POSTs. Chrome is killed, with its whole process group, before
// the profile directory is removed.
func runChrome(t *testing.T, chrome, pageURL string, results <-chan []byte) []byte {
	t.Helper()
	profile := t.TempDir()
	var output bytes.Buffer
	cmd := exec.Command(chrome,
		"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-background-networking", "--disable-sync",
		"--user-data-dir="+profile, pageURL)
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start chrome: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	done := false
	stop := func() {
		if done {
			return
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
		done = true
	}
	defer stop()

	timer := time.NewTimer(browserTimeout)
	defer timer.Stop()
	select {
	case b := <-results:
		return b
	case err := <-exited:
		done = true
		t.Fatalf("chrome exited before the page reported: %v\n%s", err, output.String())
	case <-timer.C:
		t.Fatalf("no result within %s\n%s", browserTimeout, output.String())
	}
	return nil
}

func TestBrowser(t *testing.T) {
	chrome, why := browserChrome()
	if chrome == "" {
		t.Skip(why)
	}
	other := otherOrigin(t)
	base, results, fetched, unfetched := browserSite(t, other)
	raw := runChrome(t, chrome, base+"/?"+url.Values{"other": {other}}.Encode(), results)
	var o observed
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	if o.Notes["error"] != "" {
		t.Fatalf("page error: %s", o.Notes["error"])
	}
	want := []string{
		"loaded", "cspActive", "onlyGlobal",
		"$", "$Root", "$$Array", "$$", "$$Empty",
		"morphChanged", "morphSameNode", "morphValue", "morphFocus", "morphCaret", "morphEvent",
		"replaceNew", "replaceChanged", "replaceEvent", "replaceURL",
		"fail500Replace", "fail500Morph", "fail404", "failTwo", "failText", "failDocument",
		"failNoTarget", "failNoSrc", "failNetwork",
		"failCrossOrigin", "failCrossOriginSrc", "failRedirect", "failCommentDocument", "failBodyDocument", "notPwned",
		"stillThere", "raceMorph", "raceReplace",
		"putInnerMorph", "putInnerMorphKeeps", "putInnerMorphEvent",
		"putInnerReplace", "putInnerReplaceEvent", "putInnerFragment", "putNoScript",
		"putOuter", "putOuterEvent", "putOuterMorph",
		"putTwo", "putText", "putDocument", "putNoElement", "putDetached", "putNotContent",
		"putSupersedes", "loadedTwice", "failLeft",
	}
	for _, name := range want {
		if v, ok := o.Checks[name]; !ok || !v {
			t.Errorf("check %s = %v (reported: %v); note %q", name, v, ok, o.Notes[name])
		}
	}
	if len(o.Checks) != len(want) {
		t.Errorf("%d checks reported, want %d", len(o.Checks), len(want))
	}
	// idiomorph sets a field nobody is typing into to the server's value: the
	// README says so, and this pins it.
	if got := o.Notes["otherAfterMorph"]; got != "" {
		t.Errorf("an unfocused field after morph = %q, want the server's empty value", got)
	}
	// morph, replace by element, replace by selector with a URL: three fetches of
	// /box, every one with the header.
	if f, u := fetched.Load(), unfetched.Load(); f != 3 || u != 0 {
		t.Errorf("/box fetched %d times with Collage-Fetch: 1 and %d without, want 3 and 0", f, u)
	}
	if t.Failed() {
		t.Logf("observed: %s", raw)
	}
}

func TestBrowserChrome_CISkips(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("CHROME", "")
	if err := os.Unsetenv("CHROME"); err != nil {
		t.Fatal(err)
	}
	if p, why := browserChrome(); p != "" || !strings.HasPrefix(why, "CI:") {
		t.Errorf("CI without CHROME: path %q, reason %q", p, why)
	}
	t.Setenv("CHROME", "/nonexistent")
	if p, why := browserChrome(); p != "" || !strings.HasPrefix(why, "no Chrome") {
		t.Errorf("CI with CHROME=/nonexistent: path %q, reason %q", p, why)
	}
}
