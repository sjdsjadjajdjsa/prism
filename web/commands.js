/* prism slash commands for the web.
   The command list comes from GET /api/commands (cli/commands.go `commands` is the source of truth);
   FALLBACK is used only offline / in ?demo mode. picker() is the one "choose something" overlay every
   command uses, mirroring the terminal menu (cli/menu.go drawMenu): title + hint, a filter line, rows of
   dot · label · detail · state, a "N more" line. runServer talks to cli/serve.go command(). */
(function () {
  var FALLBACK = [
    { name: "/models", help: "every model in the order prism tries them · /models <text> narrows", web: true },
    { name: "/model", help: "pick the model for this chat · /model <name> · /model auto", web: true },
    { name: "/new", help: "start a fresh chat", web: true },
    { name: "/resume", help: "reopen an earlier chat in this folder", web: true },
    { name: "/skills", help: "playbooks prism can follow · /skills <name> · /skills new <name>", web: true },
    { name: "/files", help: "browse the project and read any file", web: true },
    { name: "/theme", help: "switch the colour theme · /theme <name>", web: true },
    { name: "/beam", help: "turn the finish beam on or off", web: true },
    { name: "/quiet", help: "turn Shard's lines on or off", web: true },
    { name: "/help", help: "show these commands", web: true }
  ];
  var all = FALLBACK.slice();
  var list = FALLBACK.slice(); // what the browser can run (web: true)
  var loaded = null;

  function setList(rows) {
    all = rows;
    list = rows.filter(function (c) { return c.web !== false; });
    api.all = all; api.list = list;
  }

  // load fetches the shared command list; offline it keeps FALLBACK. load(true) fetches again when the
  // last fetch is a few seconds old, so a saved command file (.prism/commands/<name>.md) added while the
  // page is open shows up the next time "/" is typed.
  var loadedAt = 0;
  function load(fresh) {
    if (/[?&]demo\b/.test(location.search)) { if (!loaded) loaded = Promise.resolve(list); return loaded; }
    if (loaded && !(fresh && Date.now() - loadedAt > 3000)) return loaded;
    loadedAt = Date.now();
    loaded = fetch("/api/commands", { cache: "no-store" })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (rows) { if (Array.isArray(rows) && rows.length) setList(rows); return list; })
      .catch(function () { return list; });
    return loaded;
  }

  function match(text) {
    if (text.charAt(0) !== "/" || text.indexOf(" ") !== -1 || text.indexOf("\n") !== -1) return [];
    var found = [];
    for (var i = 0; i < list.length; i++) {
      if (list[i].name.indexOf(text) === 0) found.push(list[i]);
    }
    return found;
  }

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  function valueOf(input) {
    return input.value != null ? input.value : input.textContent || "";
  }

  function setValue(input, value) {
    if ("value" in input) input.value = value;
    else input.textContent = value;
    var end = value.length;
    if (input.setSelectionRange) {
      try { input.setSelectionRange(end, end); } catch (e) {}
    }
    input.dispatchEvent(new Event("input"));
  }

  // ---------- suggestion box (mirrors drawSuggestions in cli/chat.go) ----------
  function attach(opts) {
    var input = opts.input;
    var box = opts.suggestBox;
    var run = opts.run;
    var found = [], sel = -1;
    box.hidden = true;

    function paint() {
      box.textContent = "";
      if (!found.length || pickerOpen()) {
        box.hidden = true;
        input.removeAttribute("aria-activedescendant");
        input.setAttribute("aria-expanded", "false");
        return;
      }
      box.hidden = false;
      input.setAttribute("aria-expanded", "true");
      for (var i = 0; i < found.length; i++) {
        var row = el("div", "cmd-row" + (i === sel ? " sel" : ""));
        row.id = "cmd-opt-" + i;
        row.setAttribute("role", "option");
        row.setAttribute("aria-selected", String(i === sel));
        row.appendChild(el("span", "cmd-name", found[i].name));
        row.appendChild(el("span", "cmd-help", found[i].help));
        if (i === (sel < 0 ? 0 : sel)) row.appendChild(el("span", "cmd-hint", sel < 0 ? "tab" : "enter"));
        (function (c) {
          row.addEventListener("mousedown", function (ev) { ev.preventDefault(); });
          row.addEventListener("click", function () {
            setValue(input, "");
            found = []; sel = -1; paint();
            input.focus();
            if (typeof run === "function") run(c.name);
          });
        })(found[i]);
        box.appendChild(row);
      }
      if (sel >= 0) input.setAttribute("aria-activedescendant", "cmd-opt-" + sel);
      else input.removeAttribute("aria-activedescendant");
    }
    function refresh() {
      var text = valueOf(input);
      found = match(text); sel = -1; paint();
      if (text === "/") load(true).then(function () { if (valueOf(input) === text) { found = match(text); paint(); } });
    }

    input.setAttribute("aria-controls", box.id || "cmd-suggest");
    input.setAttribute("aria-autocomplete", "list");
    input.addEventListener("input", refresh);
    input.addEventListener("blur", function () { setTimeout(function () { if (document.activeElement !== input) { box.hidden = true; } }, 120); });
    input.addEventListener("focus", refresh);
    input.addEventListener("keydown", function (ev) {
      var text = valueOf(input);
      if (!box.hidden && found.length && (ev.key === "ArrowDown" || ev.key === "ArrowUp")) {
        ev.preventDefault();
        var n = found.length;
        sel = ev.key === "ArrowDown" ? (sel + 1) % n : (sel <= 0 ? n - 1 : sel - 1);
        paint();
        return;
      }
      if (ev.key === "Tab" && !ev.shiftKey) {
        if (found.length) {
          ev.preventDefault();
          setValue(input, found[sel < 0 ? 0 : sel].name + " ");
        }
        return;
      }
      if (ev.key === "Escape") {
        if (!box.hidden) { box.hidden = true; found = []; sel = -1; }
        return;
      }
      if (ev.key === "Enter" && !ev.shiftKey && !ev.ctrlKey && !ev.altKey && !ev.metaKey) {
        if (text.charAt(0) === "/") {
          ev.preventDefault();
          var line = sel >= 0 && found[sel] ? found[sel].name : text;
          found = []; sel = -1; paint();
          if (typeof run === "function") run(line);
        }
      }
    });
  }

  async function runServer(text, query) {
    var res = await fetch("/api/command" + (query || ""), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ text: text })
    });
    if (!res.ok) throw new Error(res.status + " " + ((await res.text()) || res.statusText).trim());
    return res.json();
  }

  // ---------- picker ----------
  var MAX_ROWS = 12;
  var current = null;
  var seq = 0;
  function pickerOpen() { return !!current; }
  // focusPicker puts the keys back on the open picker's filter box (a stray
  // click or a key typed on the page); false when no picker is open.
  function focusPicker() { if (!current) return false; current.focus(); return true; }
  function closePicker() { if (current) current.cancel(); }

  var chPx = 0;
  function chWidth() {
    if (!chPx) {
      var probe = el("span", "pick-row", "0000000000");
      probe.style.cssText = "position:absolute;visibility:hidden;padding:0";
      (document.querySelector(".pick") || document.body).appendChild(probe);
      chPx = probe.getBoundingClientRect().width / 10 || 8.4;
      probe.remove();
    }
    return chPx;
  }
  function keyOf(it) { return String(it.match || (it.label + " " + (it.detail || ""))).toLowerCase(); }
  function clip(s, n) { s = String(s || ""); return s.length > n ? s.slice(0, Math.max(0, n - 1)) + "…" : s; }

  /* picker(opts) opens the overlay above the input box.
     opts: title, hint, items [{label, detail, state, dot, dotTone, stateTone, sub, match, dim, toggle}],
           sel, filterable (default true), noun, empty, loading, error,
           onPick(item) — enter or a click; the picker closes first unless it returns true,
           onToggle(item) — space on a row marked toggle (the picker stays open),
           onHover(item) — the highlight landed on a row (theme preview),
           onClose() — esc, a click outside, or another picker replacing this one; not called after a pick.
     Returns { update(opts), close(), el }. Focus goes back to opts.returnFocus (the chat input) on close. */
  function picker(opts) {
    if (current) current.cancel();
    var id = "pick-" + (++seq);
    var o = Object.assign({ filterable: true, items: [], sel: 0 }, opts);
    var anchor = o.anchor || document.getElementById("inputbox") || document.body;
    var back = o.returnFocus || document.getElementById("input");
    var filter = "", sel = o.sel || 0, vis = [], closed = false, lastHover = null;

    var root = el("div", "pick");
    root.setAttribute("role", "dialog");
    var head = el("div", "pick-head");
    var titleEl = el("span", "pick-title");
    var hintEl = el("span", "pick-hint");
    head.append(titleEl, hintEl);
    var fline = el("label", "pick-filter");
    var caret = el("span", "pick-caret", "›");
    caret.setAttribute("aria-hidden", "true");
    var box = el("input", "pick-input");
    box.type = "text";
    box.autocomplete = "off";
    box.spellcheck = false;
    box.placeholder = "type to filter";
    box.setAttribute("role", "combobox");
    box.setAttribute("aria-expanded", "true");
    box.setAttribute("aria-controls", id + "-list");
    box.setAttribute("aria-autocomplete", "list");
    fline.append(caret, box);
    var listEl = el("div", "pick-list");
    listEl.id = id + "-list";
    listEl.setAttribute("role", "listbox");
    var msgEl = el("div", "pick-msg");
    msgEl.setAttribute("role", "status");
    root.append(head, fline, listEl, msgEl);

    function filtered() {
      if (!filter) return o.items.slice();
      var f = filter.toLowerCase();
      return o.items.filter(function (it) { return keyOf(it).indexOf(f) !== -1; });
    }

    function hover() {
      var it = vis[sel];
      if (it && it !== lastHover && typeof o.onHover === "function") { lastHover = it; o.onHover(it); }
    }

    function render() {
      titleEl.textContent = o.title || "";
      hintEl.textContent = "  " + (o.hint || "↑↓ move · enter pick · esc close");
      root.setAttribute("aria-label", o.title || "pick one");
      listEl.setAttribute("aria-label", o.title || "choices");
      fline.hidden = !o.filterable;
      vis = filtered();
      if (sel >= vis.length) sel = vis.length - 1;
      if (sel < 0) sel = 0;
      listEl.textContent = "";
      var nameW = 10, detW = 0;
      vis.forEach(function (it) {
        if (!it.sub) { nameW = Math.max(nameW, String(it.label || "").length); detW = Math.max(detW, String(it.detail || "").length); }
      });
      // like drawMenu: the label column first, the detail gets what is left beside the state
      var cols = Math.floor((root.clientWidth - 24) / chWidth()) || 80;
      var stateW = vis.reduce(function (n, it) { return Math.max(n, String(it.state || "").length); }, 0);
      // a phone (~40 columns) keeps most of the row for the name: the state
      // column may only claim 10 there, and the detail gets what is left
      var stateRoom = Math.min(stateW, cols < 60 ? 10 : 24);
      nameW = Math.max(6, Math.min(nameW, cols - 4 - stateRoom - (detW ? 8 : 0)));
      detW = Math.max(0, Math.min(detW, cols - nameW - 6 - stateRoom));
      vis.forEach(function (it, i) {
        var row = el("div", "pick-row" + (it.sub ? " sub" : "") + (it.dim ? " dim" : "") + (i === sel ? " sel" : ""));
        row.id = id + "-opt-" + i;
        row.setAttribute("role", "option");
        row.setAttribute("aria-selected", String(i === sel));
        if (it.sub) {
          row.append(el("span", "pick-dot muted", "↳"), el("span", "pick-label", it.label));
        } else {
          var dot = el("span", "pick-dot tone-" + (it.dotTone || "muted"), it.dot || " ");
          dot.setAttribute("aria-hidden", "true");
          var label = el("span", "pick-label", clip(it.label, nameW));
          label.style.width = (nameW + 2) + "ch";
          if (String(it.label || "").length > nameW) label.title = it.label;
          row.append(dot, label);
          if (detW > 0) {
            var det = el("span", "pick-detail", clip(it.detail, detW));
            det.style.width = (detW + 2) + "ch";
            if (String(it.detail || "").length > detW) det.title = it.detail;
            row.append(det);
          }
          if (it.state) row.append(el("span", "pick-state tone-" + (it.stateTone || "muted"), it.state));
        }
        row.addEventListener("mousemove", function () { if (sel !== i) { sel = i; mark(); hover(); } });
        row.addEventListener("mousedown", function (ev) { ev.preventDefault(); });
        row.addEventListener("click", function () { sel = i; mark(); choose(); });
        listEl.appendChild(row);
      });
      var msg = "", err = false;
      if (o.loading && !o.items.length) msg = "loading…";
      else if (o.error && !o.items.length) { msg = "✕ " + o.error; err = true; }
      else if (!vis.length && filter) msg = "no " + (o.noun || "row") + " matches \u201c" + filter + "\u201d";
      else if (!vis.length) msg = o.empty || "nothing here yet";
      else if (vis.length > MAX_ROWS) msg = (vis.length - MAX_ROWS) + " more · scroll" + (o.filterable ? " or type to narrow" : "");
      msgEl.textContent = msg;
      msgEl.hidden = !msg;
      msgEl.classList.toggle("err", err);
      listEl.hidden = !vis.length;
      mark();
    }

    function mark() {
      var rows = listEl.children;
      for (var i = 0; i < rows.length; i++) {
        var on = i === sel;
        rows[i].classList.toggle("sel", on);
        rows[i].setAttribute("aria-selected", String(on));
      }
      var row = rows[sel];
      if (row) {
        box.setAttribute("aria-activedescendant", row.id);
        var top = row.offsetTop, bottom = top + row.offsetHeight;
        if (top < listEl.scrollTop) listEl.scrollTop = top;
        else if (bottom > listEl.scrollTop + listEl.clientHeight) listEl.scrollTop = bottom - listEl.clientHeight;
      } else box.removeAttribute("aria-activedescendant");
    }

    function move(to) {
      if (!vis.length) return;
      sel = Math.max(0, Math.min(vis.length - 1, to));
      mark(); hover();
    }

    function teardown() {
      if (closed) return;
      closed = true;
      document.removeEventListener("mousedown", outside, true);
      if (back && back.removeEventListener) back.removeEventListener("focus", backFocus);
      root.remove();
      if (current === handle) current = null;
      if (back && back.focus) back.focus();
    }

    function cancel() {
      if (closed) return;
      teardown();
      if (typeof o.onClose === "function") o.onClose();
    }

    function choose() {
      var it = vis[sel];
      if (!it) return;
      teardown();
      if (typeof o.onPick === "function") o.onPick(it);
    }

    function outside(ev) { if (!root.contains(ev.target)) cancel(); }
    // the chat box got the keys (tab, a typed letter): the picker is done, like a click outside,
    // so Enter there can never send a chat line under an open picker
    function backFocus() { if (!closed) cancel(); }
    function refocus() { if (!closed && document.activeElement !== box) box.focus({ preventScroll: true }); }
    // a click on the title, hint, "N more" line or the list scrollbar must not take the keys away
    root.addEventListener("mouseup", refocus);
    root.addEventListener("mousedown", function (ev) { if (ev.target !== box && ev.target !== listEl) ev.preventDefault(); });

    box.addEventListener("keydown", function (ev) {
      var n = vis.length;
      switch (ev.key) {
        case "ArrowDown": ev.preventDefault(); if (n) move((sel + 1) % n); break;
        case "ArrowUp": ev.preventDefault(); if (n) move((sel - 1 + n) % n); break;
        case "PageDown": ev.preventDefault(); move(sel + MAX_ROWS); break;
        case "PageUp": ev.preventDefault(); move(sel - MAX_ROWS); break;
        case "Home": if (!filter) { ev.preventDefault(); move(0); } break;
        case "End": if (!filter) { ev.preventDefault(); move(n - 1); } break;
        case "Enter": ev.preventDefault(); ev.stopPropagation(); choose(); break;
        case "Escape": ev.preventDefault(); ev.stopPropagation(); cancel(); break;
        case "Tab": ev.preventDefault(); if (n) move(ev.shiftKey ? (sel - 1 + n) % n : (sel + 1) % n); break;
        case " ":
          if (vis[sel] && vis[sel].toggle && typeof o.onToggle === "function") {
            ev.preventDefault();
            o.onToggle(vis[sel]);
            render();
          } else if (!o.filterable) ev.preventDefault();
          break;
        default: break;
      }
    });
    box.addEventListener("input", function () {
      if (!o.filterable) { box.value = ""; return; }
      filter = box.value;
      sel = 0; listEl.scrollTop = 0;
      render(); hover();
    });

    var handle = {
      el: root,
      cancel: cancel,
      close: teardown,
      focus: refocus,
      update: function (more) { Object.assign(o, more); if (more.sel != null) sel = more.sel; if (!closed) { render(); hover(); } },
      get open() { return !closed; }
    };
    current = handle;
    anchor.appendChild(root);
    var sug = document.getElementById("cmd-suggest");
    if (sug) sug.hidden = true;
    render();
    box.focus();
    if (back && back.addEventListener && back !== box) back.addEventListener("focus", backFocus);
    setTimeout(function () { if (!closed) document.addEventListener("mousedown", outside, true); }, 0);
    hover();
    return handle;
  }

  var api = { list: list, all: all, load: load, match: match, attach: attach, runServer: runServer, picker: picker, pickerOpen: pickerOpen, focusPicker: focusPicker, closePicker: closePicker };
  window.PrismCommands = api;
  load();
})();
