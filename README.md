# elagoht/client

A collage plugin that serves a tiny client script with three things in it:
element getters, and a fragment fetched from its own URL put in place, either by
replacing it or by morphing it. Everything else is left to plain JavaScript.

```sh
collage plugin add elagoht/client
```

or by hand:

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{client.New()},
})
```

Then one call in the master layout:

```html
<head>
  {{clientjs}}
</head>
```

Requires collage v0.66.0 or later. Register it in `Config.Plugins`: it adds a
template function, which only a plugin registered there can. There is nothing to
configure.

## The API

The script defines `window.collage`, and nothing else.

### `collage.$(selector, root?)` and `collage.$$(selector, root?)`

`$` is `querySelector`, `$$` is `querySelectorAll` returned as an Array, both on
`document` unless `root` is given.

```js
const form = collage.$("#add");
const names = collage.$$("li", collage.$("#items")).map((li) => li.textContent);
```

### `collage.replace(target, url?)`

Fetches a fragment and puts it in place of `target`, as `outerHTML` would.
`target` is a selector or an Element; `url` defaults to the element's `data-src`,
where a page opened with `WithFragmentPath` puts `{{fragmentURL}}`. It resolves to
the new element.

### `collage.morph(target, url?)`

The same fetch, but the DOM is patched in place with
[idiomorph](https://github.com/bigskysoftware/idiomorph): elements that match are
kept, so the field being typed into keeps its focus, its caret and what is typed in
it, and a kept element keeps its scroll position. It resolves to the element now in
the page — the same node, when the fragment's root has the same tag and id.

A field the reader is not focused on takes the server's value, as idiomorph does:
what was typed into another field of the morphed element is replaced by the
fragment's markup. Morph a part of the page that does not hold a half-filled form,
or use `replace` and accept that it starts afresh.

### The `collage:swap` event

After either one puts the fragment in, a `collage:swap` event bubbles from the new
element, with `detail: { url, mode }` and `mode` `"replace"` or `"morph"`:

```js
document.addEventListener("collage:swap", (event) => {
  console.log(event.target.id, event.detail.mode, event.detail.url);
});
```

### The request

A `GET` of `url` with the header `Collage-Fetch: 1` (`collage.FetchHeader`) and
the page's cookies (`credentials: "same-origin"`), as collage's fragment paths and
actions expect.

## The shopping list, again

The [Update part of a page](https://collage.furkanbaytekin.dev/en/docs/update-part-of-a-page/)
recipe opens the list at `/list/items`, marks it

```html
<ul id="items" data-src="{{fragmentURL "list" "items"}}">
```

and keeps it current with a `setInterval` of `fetch` and `outerHTML`. With the
plugin, that half is one line, and a morph leaves alone whatever the reader is
doing on the page:

```js
setInterval(() => collage.morph("#items").catch(() => {}), 5000);
```

The submit half keeps its own `fetch`: it posts the form, and `replace` and
`morph` fetch with `GET`. It can still use the getters:

```js
const form = collage.$("#add");

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    const response = await fetch(form.action, {
      method: "POST",
      headers: { "Collage-Fetch": "1" },
      body: new FormData(form),
    });
    const items = collage.$("#items");
    if (!response.ok || !items) throw new Error(`answered ${response.status}`);
    items.outerHTML = await response.text();
    form.reset();
  } catch {
    form.submit();
  }
});
```

## When it fails

A target that is not in the page, an element with no `data-src` and no `url`, a
network error, an answer that is not 2xx, or an answer that is not a single element
— a plain-text error body, a whole error page, two elements side by side — leaves
the page as it was, fires no event, and rejects with an `Error` that says which:

```
collage: morph /list/items: answered 500
collage: replace /list/items: the answer is not a single element
collage: replace: no element #items
```

So a caller that wants a fallback catches it, and one that does not can let a
polling tick fail quietly with `.catch(() => {})`.

Scripts inside a fetched fragment do not run, as with `outerHTML`.

## Content-Security-Policy

With [elagoht/secure](https://github.com/Elagoht/collage-secure) setting a
nonce, pass it:

```html
{{clientjs cspNonce}}
```

The nonce is an argument because `{{cspNonce}}` renders a placeholder that
collage-secure replaces with a fresh nonce on every response, after the page cache;
there is nothing in the render for the plugin to read it from. A policy that allows
`'self'` loads the script without it — it is an external, same-origin file — so the
nonce matters under a nonce-only or `'strict-dynamic'` policy. A static build
removes the nonce attribute, as it does every placeholder nonce.

## The file

The script is served under `/_client/` at a content-addressed name,
`/_client/client.<hash>.js`, through collage's own fingerprinting: it is cached for
a year with `immutable`, a new release is a new URL, and a hash that is not the
file's own is a 404. It is loaded with `defer`, so it runs before
`DOMContentLoaded`, after the page is parsed. In development it is served
`no-store`, like every mount.

It is about 14 KB, 5.5 KB gzipped; the hand-written part is under 3 KB, the rest is
idiomorph and its license notice. It depends on nothing.

## Credits

Morphing is [idiomorph](https://github.com/bigskysoftware/idiomorph) 0.8.0 by Big
Sky Software, under the Zero-Clause BSD license, bundled unchanged from the npm
package's `dist/idiomorph.min.js`; its notice and version are at the top of
`idiomorph.js` and of the served script.
