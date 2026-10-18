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
// elements, loose text (a plain-text error body), or a whole document.
const parse = (html) => {
  if (/^\s*<(!doctype|html)[\s>]/i.test(html)) return null;
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

const swap = async (mode, target, url) => {
  const old = typeof target === "string" ? $(target) : target;
  if (!(old instanceof Element)) return fail(mode + ": no element " + (typeof target === "string" ? target : "given"));
  const src = url || old.getAttribute("data-src");
  if (!src) return fail(mode + ": no url, and the element has no data-src");
  let response, html;
  try {
    response = await fetch(src, { headers: { "Collage-Fetch": "1" }, credentials: "same-origin" });
    html = await response.text();
  } catch (e) {
    return fail(mode + " " + src + ": " + (e && e.message ? e.message : e), e);
  }
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
};

window.collage = Object.freeze({
  $,
  $$,
  replace: (target, url) => swap("replace", target, url),
  morph: (target, url) => swap("morph", target, url),
});
