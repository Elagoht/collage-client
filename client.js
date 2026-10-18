// collage-client: element getters, and a fragment fetched from its own URL put in
// place. Served as one script after idiomorph, inside the same function, so the
// only name it leaves on the page is window.collage.
"use strict";

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
  let element = null;
  for (const node of t.content.childNodes) {
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

// pending is each element's call in flight. The newest call on an element wins:
// it aborts the one before, which rejects as superseded.
const pending = new WeakMap();

const swap = async (mode, target, url) => {
  const old = typeof target === "string" ? $(target) : target;
  if (!(old instanceof Element)) return fail(mode + ": no element " + (typeof target === "string" ? target : "given"));
  const src = url || old.getAttribute("data-src");
  if (!src) return fail(mode + ": no url, and the element has no data-src");
  if (!sameOrigin(src)) return fail(mode + " " + src + ": not this site's origin");
  const own = new AbortController();
  const before = pending.get(old);
  if (before) before.abort();
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
    let result = fresh;
    if (mode === "replace") {
      old.replaceWith(fresh);
    } else {
      // The value of the field being typed into is the reader's, not the server's.
      const nodes = Idiomorph.morph(old, fresh, { morphStyle: "outerHTML", ignoreActiveValue: true });
      result = nodes.find((n) => n.nodeType === Node.ELEMENT_NODE) || fresh;
    }
    result.dispatchEvent(new CustomEvent("collage:swap", { bubbles: true, detail: { url: src, mode } }));
    return result;
  } finally {
    if (pending.get(old) === own) pending.delete(old);
  }
};

window.collage = Object.freeze({
  $,
  $$,
  replace: (target, url) => swap("replace", target, url),
  morph: (target, url) => swap("morph", target, url),
});
