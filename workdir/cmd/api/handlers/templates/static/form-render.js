// form-render.js renders the fields of a form's content. It is shared by the
// form builder (its canvas and preview) and is served at
// GET /static/form-render.js; it exposes window.FormRender.
(() => {
  "use strict";

  const CONTROL = "w-full rounded-lg border border-slate-800 bg-slate-950 px-3 py-2 text-sm text-slate-300 placeholder:text-slate-600";

  // h creates an element; string children become text nodes, so user
  // input is never parsed as HTML.
  const h = (tag, props, ...children) => {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v == null || v === false) continue;
      if (k === "class") el.className = v;
      else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else if (k in el && k !== "value") el[k] = v;
      else if (k !== "value") el.setAttribute(k, v === true ? "" : v);
    }
    el.append(...children.flat().filter((c) => c != null && c !== false));
    // A select only accepts a value once its options exist.
    if (props && props.value != null) el.value = props.value;
    return el;
  };

  // control renders the control of field f. By default it is a static
  // picture (the builder's canvas); with opts.live it is a working form
  // control whose element ID is opts.id.
  const control = (f, opts) => {
    const live = !!(opts && opts.live);
    const ph = f.placeholder || null;
    const attrs = live ? { id: opts.id, name: f.name, required: f.required } : { tabIndex: -1 };
    const input = (tag, props, ...children) => h(tag, Object.assign({}, attrs, props), ...children);
    const source = (text) => h("p", { class: "font-mono text-[11px] text-slate-500 truncate" }, text);
    switch (f.type) {
      case "textfield":
        return input("input", { type: f.inputType, placeholder: ph, class: CONTROL });
      case "textarea":
        return input("textarea", { rows: f.rows, placeholder: ph, class: CONTROL + " resize-none" });
      case "select":
        return input("select", { class: CONTROL },
          h("option", { value: "" }, ph || "Select an option…"),
          f.options.map((opt) => h("option", { value: opt.value }, opt.label)));
      case "select-data":
        return h("div", { class: "space-y-1.5" },
          input("select", { class: CONTROL }, h("option", { value: "" }, ph || "Select an option…")),
          source("options ← " + (f.source.url || "(no URL yet)")));
      case "autocomplete-data":
        return h("div", { class: "space-y-1.5" },
          input("input", { type: "search", placeholder: ph || "Start typing to search…", class: CONTROL }),
          source("suggestions ← " + (f.source.url || "(no URL yet)") + "?" + f.source.searchParam + "=… after " + f.minChars + " characters"));
      case "file": {
        const limits = (f.accept || "Any file type") + " · up to " + f.maxSizeMb + " MB" + (f.multiple ? " each" : "");
        if (live) {
          return h("div", { class: "rounded-xl border-2 border-dashed border-slate-700 px-4 py-4" },
            input("input", { type: "file", accept: f.accept || null, multiple: f.multiple, class: "block w-full text-sm text-slate-400 file:mr-3 file:rounded-lg file:border-0 file:bg-slate-800 file:px-3 file:py-2 file:text-xs file:font-medium file:text-slate-200 hover:file:bg-slate-700" }),
            h("p", { class: "mt-2 text-[11px] text-slate-500" }, limits));
        }
        return h("div", { class: "rounded-xl border-2 border-dashed border-slate-700 px-4 py-6 text-center" },
          h("p", { class: "text-sm text-slate-300" }, f.multiple ? "Drop files here or browse" : "Drop a file here or browse"),
          h("p", { class: "mt-1 text-[11px] text-slate-500" }, limits));
      }
      case "toggle":
        if (live) {
          return h("label", { class: "inline-flex cursor-pointer items-center gap-3" },
            input("input", { type: "checkbox", checked: f.defaultValue, class: "peer sr-only" }),
            h("span", { class: "relative h-6 w-11 shrink-0 rounded-full bg-slate-700 transition peer-checked:bg-indigo-600 peer-focus-visible:ring-2 peer-focus-visible:ring-indigo-400 after:absolute after:left-0.5 after:top-0.5 after:h-5 after:w-5 after:rounded-full after:bg-white after:shadow after:transition-all peer-checked:after:translate-x-5" }),
            h("span", { class: "text-sm text-slate-400" }, "Yes"));
        }
        return h("div", { class: "flex items-center gap-3" },
          h("span", { class: "relative inline-flex h-6 w-11 shrink-0 rounded-full transition " + (f.defaultValue ? "bg-indigo-600" : "bg-slate-700") },
            h("span", { class: "absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all " + (f.defaultValue ? "left-[1.375rem]" : "left-0.5") })),
          h("span", { class: "text-sm text-slate-400" }, f.defaultValue ? "Yes" : "No"));
    }
    return null;
  };

  const INPUT_TYPES = ["text", "email", "number", "tel", "url", "date"];

  // fromContent maps a field of the stored content JSON (snake_case keys)
  // to the shape control and field take. Missing or mistyped properties get
  // the builder's defaults; panels map their nested fields too.
  const fromContent = (o) => {
    o = o && typeof o === "object" ? o : {};
    const str = (v, def) => (typeof v === "string" ? v : def);
    const num = (v, def) => (Number.isFinite(v) ? v : def);
    const src = o.data_source && typeof o.data_source === "object" ? o.data_source : {};
    const f = {
      type: str(o.type, ""), name: str(o.name, ""), label: str(o.label, ""), help: str(o.help, ""),
      required: o.required === true, placeholder: str(o.placeholder, ""),
      inputType: INPUT_TYPES.includes(o.input_type) ? o.input_type : "text", rows: num(o.rows, 4),
      options: Array.isArray(o.options)
        ? o.options.map((opt) => ({ label: str(opt && opt.label, ""), value: str(opt && opt.value, "") }))
        : [],
      source: { url: str(src.url, ""), labelKey: str(src.label_key, "name"), valueKey: str(src.value_key, "id"), searchParam: str(src.search_param, "q") },
      minChars: num(o.min_chars, 2), accept: str(o.accept, ""), multiple: o.multiple === true,
      maxSizeMb: num(o.max_size_mb, 10), defaultValue: o.default === true,
    };
    switch (f.type) {
      case "panel":
        f.title = str(o.title, "");
        f.fields = Array.isArray(o.fields) ? o.fields.map(fromContent) : [];
        break;
      case "content": f.text = str(o.text, ""); break;
      case "button": f.url = str(o.url, ""); break;
    }
    return f;
  };

  // safeURL resolves a button URL, keeping only http(s) links so content
  // can never inject a javascript: or data: URL.
  const safeURL = (u) => {
    if (!u) return null;
    try {
      const url = new URL(u, document.baseURI);
      return url.protocol === "https:" || url.protocol === "http:" ? url.href : null;
    } catch (_) {
      return null;
    }
  };

  const BUTTON = "inline-flex items-center rounded-lg border border-slate-700 px-4 py-2 text-sm font-medium text-slate-200 hover:bg-slate-800";

  const LAYOUT = ["panel", "content", "button"];

  // field renders field f as respondents see it: its label (marked when
  // required), help text and live control, whose element ID is id (and its
  // wrapper's data-field attribute, where pages show field errors). The
  // layout-only types render as a titled group of their nested fields
  // (panel, whose fields get IDs prefix + name), static text (content) or a
  // non-submitting button (button).
  const field = (f, id, prefix) => {
    switch (f.type) {
      case "panel":
        return h("fieldset", { class: "space-y-5 rounded-xl border border-slate-800 p-4" },
          f.title ? h("legend", { class: "px-1 text-sm font-semibold text-white break-words" }, f.title) : null,
          fields(f.fields || [], prefix != null ? prefix : id + "-"));
      case "content":
        return h("p", { class: "whitespace-pre-line text-sm text-slate-300 break-words" }, f.text || "");
      case "button": {
        const href = safeURL(f.url);
        const label = f.label || "Button";
        return h("div", null, href
          ? h("a", { href: href, target: "_blank", rel: "noopener noreferrer", class: BUTTON }, label)
          : h("button", { type: "button", class: BUTTON }, label));
      }
    }
    return h("div", { class: "space-y-1.5", "data-field": id },
      h(f.type === "toggle" ? "p" : "label", { class: "block text-sm font-semibold text-white break-words", htmlFor: f.type === "toggle" ? null : id },
        f.label || "Untitled field", f.required ? h("span", { class: "text-rose-400" }, " *") : null),
      f.help ? h("p", { class: "text-xs text-slate-400 break-words" }, f.help) : null,
      control(f, { live: true, id: id }));
  };

  // fields renders a list of fields (in field's shape). Input fields, nested
  // in panels or not, get the element ID prefix + name, as names are unique
  // across a form.
  const fields = (list, prefix) =>
    list.map((f, i) => field(f, prefix + (LAYOUT.includes(f.type) ? "layout-" + i : f.name), prefix));

  // renderContent renders the fields of a stored form content JSON
  // ({version, fields}) as live controls with IDs prefix + name.
  const renderContent = (content, prefix) =>
    fields(content && Array.isArray(content.fields) ? content.fields.map(fromContent) : [], prefix || "field-");

  window.FormRender = Object.freeze({ h: h, control: control, field: field, fields: fields, fromContent: fromContent, renderContent: renderContent });
})();
