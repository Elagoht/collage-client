// collage-client: element getters, and a fragment put in place, fetched from its
// own URL or handed over as markup. Served as one script after idiomorph, inside
// the same function, so the only name it leaves on the page is window.collage.
"use strict";

// A second copy of the script — a layout calling {{clientjs}} and a plugin that
// includes it too — keeps the first: its calls in flight are the ones to win.
if (window.collage) return;

const $ = (selector, root) => (root || document).querySelector(selector);
const $$ = (selector, root) => Array.from((root || document).querySelectorAll(selector));

// fail is the rejection every failure ends in: nothing has been touched.
const fail = (message, cause) =>
  Promise.reject(new Error("collage: " + message, cause ? { cause } : undefined));

// parse returns the one element html holds, or null for anything else: several
// elements, loose text (a plain-text error body), or a whole document. A
// <template> drops <html>, <head> and <body> and keeps what was in them, so an
// error page would pass for a fragment: any of those tags refuses it.
const parse = (html) => {
  if (/^\s*<!doctype[\s>]/i.test(html) || /<(html|head|body)[\s>/]/i.test(html)) return null;
  const t = document.createElement("template");
  t.innerHTML = html;
  return only(t.content);
};

// only returns the one element among parent's children, or null when there are
// several, or loose text beside it.
const only = (parent) => {
  let element = null;
  for (const node of parent.childNodes) {
    if (node.nodeType === Node.ELEMENT_NODE) {
      if (element) return null;
      element = node;
    } else if (node.nodeType === Node.TEXT_NODE && node.data.trim() !== "") {
      return null;
    }
  }
  return element;
};

// sameOrigin reports whether url, resolved against the page, is on this site:
// what is fetched is inserted as markup, so it is only ever this site's own.
const sameOrigin = (url) => {
  try {
    return new URL(url, location.href).origin === location.origin;
  } catch {
    return false;
  }
};

// pending is each element's fetch in flight. The newest call on an element wins:
// it aborts the one before, which rejects as superseded.
const pending = new WeakMap();

const supersede = (el) => {
  const before = pending.get(el);
  if (before) before.abort();
  pending.delete(el);
};

const elementOf = (target) => (typeof target === "string" ? $(target) : target);
const named = (target) => (typeof target === "string" ? target : "given");

// single returns the one element content holds, or null: markup goes through
// parse, an Element is itself, and a DocumentFragment must hold one element and
// no loose text.
const single = (content) => {
  if (typeof content === "string") return parse(content);
  if (content instanceof Element) return content;
  if (content instanceof DocumentFragment) return only(content);
  return null;
};

// nodes returns what content holds, to go inside an element: markup parsed by a
// <template>, so its scripts do not run, or the node itself.
const nodes = (content) => {
  if (typeof content !== "string") return content;
  const t = document.createElement("template");
  t.innerHTML = content;
  return t.content;
};

// place puts content in place of old, or inside it, and fires collage:swap. It is
// synchronous: when it returns, the page shows the content. It throws, having
// touched nothing, when the content does not fit.
const place = (mode, old, content, inner, url) => {
  let result;
  if (inner) {
    if (content == null || !(typeof content === "string" || content instanceof Node)) {
      throw new Error("collage: put: the content is not markup or a node");
    }
    if (mode === "replace") old.replaceChildren(nodes(content));
    // The value of the field being typed into is the reader's, not the server's.
    else Idiomorph.morph(old, nodes(content), { morphStyle: "innerHTML", ignoreActiveValue: true });
    result = old;
  } else {
    const fresh = single(content);
    if (!fresh) throw new Error("collage: " + (url ? mode + " " + url : "put") + ": the answer is not a single element");
    if (mode === "replace") {
      old.replaceWith(fresh);
      result = fresh;
    } else {
      const morphed = Idiomorph.morph(old, fresh, { morphStyle: "outerHTML", ignoreActiveValue: true });
      result = morphed.find((n) => n.nodeType === Node.ELEMENT_NODE) || fresh;
    }
  }
  result.dispatchEvent(new CustomEvent("collage:swap", { bubbles: true, detail: { url: url || null, mode, inner } }));
  return result;
};

const swap = async (mode, target, url) => {
  const old = elementOf(target);
  if (!(old instanceof Element)) return fail(mode + ": no element " + named(target));
  const src = url || old.getAttribute("data-src");
  if (!src) return fail(mode + ": no url, and the element has no data-src");
  if (!sameOrigin(src)) return fail(mode + " " + src + ": not this site's origin");
  const own = new AbortController();
  supersede(old);
  pending.set(old, own);
  const superseded = () => fail(mode + " " + src + ": superseded by a newer call on the element");
  try {
    let response, html;
    try {
      response = await fetch(src, { headers: { "Collage-Fetch": "1" }, credentials: "same-origin", signal: own.signal });
      html = await response.text();
    } catch (e) {
      if (own.signal.aborted) return superseded();
      return fail(mode + " " + src + ": " + (e && e.message ? e.message : e), e);
    }
    if (own.signal.aborted) return superseded();
    if (!sameOrigin(response.url)) return fail(mode + " " + src + ": redirected off this site's origin");
    if (!response.ok) return fail(mode + " " + src + ": answered " + response.status);
    const fresh = parse(html);
    if (!fresh) return fail(mode + " " + src + ": the answer is not a single element");
    if (!old.isConnected) return fail(mode + " " + src + ": the element left the page while it was fetched");
    return place(mode, old, fresh, false, src);
  } finally {
    if (pending.get(old) === own) pending.delete(old);
  }
};

// put is the swap without the fetch: the content is already in hand. It changes
// the page before it returns, and aborts a replace or morph in flight on the
// element, so that older answer cannot land on top.
const put = (target, content, options) => {
  const old = elementOf(target);
  if (!(old instanceof Element)) throw new Error("collage: put: no element " + named(target));
  if (!old.isConnected) throw new Error("collage: put: the element is not in the page");
  const opts = options || {};
  supersede(old);
  return place(opts.morph ? "morph" : "replace", old, content, !!opts.inner);
};

window.collage = Object.freeze({
  $,
  $$,
  replace: (target, url) => swap("replace", target, url),
  morph: (target, url) => swap("morph", target, url),
  put,
});
