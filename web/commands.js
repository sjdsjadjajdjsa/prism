/* prism slash commands for the web.
   The command list comes from GET /api/commands; FALLBACK is used only offline or in ?demo mode.
   picker() is the one "choose something" overlay every command uses: title and hint, a filter
   line, rows of dot · label · detail · state, and a "N more" line. */
(function () {
  var FALLBACK = [
    { name: "/models", help: "every model in the order prism tries them · /models <text> narrows", web: true },
    { name: "/model", help: "pick the model for this chat · /model <name> · /model auto", web: true },
    { name: "/new", help: "start a fresh chat", web: true },
    { name: "/resume", help: "reopen an earlier chat in this folder", web: true },
    { name: "/skills", help: "playbooks prism can follow · /skills <name> · /skills new <name>", web: true },
    { name: "/files", help: "find a file by name and read it", web: true },
    { name: "/map", help: "a map of how the files link, in the browser", web: true },
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

  // PICKERS open a picker when run bare. Taking one from the suggestion list runs it, tab too,
  // so the picker opens without a second enter.
  var PICKERS = { "/model": 1, "/login": 1, "/key": 1, "/resume": 1, "/theme": 1, "/tabs": 1 };
  // other names commands answer to; enter runs them as typed
  var ALIASES = ["/exit", "/status", "/continue", "/keys", "/logins", "/themes", "/forget"];

  // Keep in sync with rankCommand in cli/commands.go. Returns [tier, score] or null. Tier 0 is
  // the exact name, 1 a prefix (shorter first; a bare "/" keeps the list order), 2 a close match:
  // the query inside the name ("odel"), then its letters in order ("mdl"). Lower scores sort first.
  function rank(name, q) {
    var n = name.replace(/^\//, "").toLowerCase();
    if (q === "") return [1, 0];
    if (n === q) return [0, 0];
    if (n.indexOf(q) === 0) return [1, n.length];
    if (q.length < 2) return null;
    var at = n.indexOf(q);
    if (at > 0) return [2, 100 + at * 10 + n.length];
    var k = 0, first = -1, last = -1;
    for (var i = 0; i < n.length && k < q.length; i++) {
      if (n.charAt(i) === q.charAt(k)) { if (first < 0) first = i; last = i; k++; }
    }
    if (k < q.length) return null;
    return [2, 1000 + (last - first + 1 - q.length) * 10 + first * 5 + n.length];
  }

  // match is the suggestion list for what is typed so far: rows of {name, help, saved, close},
  // exact match first, then prefixes, then close matches.
  function match(text) {
    if (text.charAt(0) !== "/" || text.indexOf("\n") !== -1) return [];
    if (text.indexOf(" ") !== -1) return options(text);
    var q = text.slice(1).toLowerCase(), ranked = [];
    for (var i = 0; i < list.length; i++) {
      var r = rank(list[i].name, q);
      if (r) ranked.push({ c: list[i], tier: r[0], score: r[1], i: i });
    }
    ranked.sort(function (a, b) { return a.tier - b.tier || a.score - b.score || a.i - b.i; });
    return ranked.map(function (f) { return { name: f.c.name, help: f.c.help || "", saved: !!f.c.saved, close: f.tier === 2 }; });
  }

  // isName: text is a whole command name (listed or an alias); enter runs it as typed
  // options lists a command's next words once "/name " is typed (Keep in sync with
  // optionSuggestions in cli/commands.go): words starting with the rest first, then words containing it.
  function options(text) {
    var sp = text.indexOf(" "), name = text.slice(0, sp), q = text.slice(sp + 1).toLowerCase();
    if (q.indexOf(" ") !== -1) return [];
    var c = null;
    for (var i = 0; i < list.length; i++) if (list[i].name === name) c = list[i];
    if (!c || !c.options) return [];
    var first = [], later = [];
    c.options.forEach(function (o) {
      var w = o.word.toLowerCase(), row = { name: name + " " + o.word, help: o.help || "", option: true };
      if (q && w === q) first.unshift(row);
      else if (w.indexOf(q) === 0) first.push(row);
      else if (w.indexOf(q) !== -1) later.push(row);
    });
    return first.concat(later);
  }

  function isName(text) {
    if (ALIASES.indexOf(text) !== -1) return true;
    for (var i = 0; i < all.length; i++) if (all[i].name === text) return true;
    return false;
  }

  // Keep in sync with splitHelp in cli/commands.go. "pick the model · /model <name>" becomes ["pick the model", "/model <name>"].
  function splitHelp(help) {
    var k = help.indexOf(" · ");
    return k < 0 ? [help, ""] : [help.slice(0, k), help.slice(k + 3)];
  }

  // hintFor is the usage hint, or what taking the command does.
  function hintFor(c) {
    var usage = splitHelp(c.help)[1];
    if (usage) return usage;
    if (c.option) return c.name + " · enter runs it";
    if (PICKERS[c.name]) return c.name + " · enter opens the list";
    if (c.saved) return c.name + " <words> · sends your saved prompt";
    return c.name + " · enter runs it";
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

  // Suggestion box. Typing "/" opens it above the input with the matching commands and a
  // highlight on the best match. ↑↓ move it (wrapping) and the pointer follows. tab or enter
  // takes the highlighted command, a click runs it, esc closes the list and keeps what is typed;
  // any edit reopens it.
  var MAX_SUGGEST = 10; // list lines before it scrolls
  function attach(opts) {
    var input = opts.input;
    var box = opts.suggestBox;
    var found = [], sel = 0, closed = false, typed = null;
    var rows = [], listEl = null, usageEl = null, keysEl = null, lastMove = "";
    hide();

    function hide() {
      box.hidden = true;
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
    }

    function run(text) {
      found = []; sel = 0; typed = null;
      hide();
      if (typeof opts.run === "function") opts.run(text);
    }

    function open() { return found.length > 0 && !closed && !pickerOpen(); }

    function paint() {
      box.textContent = "";
      rows = [];
      if (!open()) {
        hide();
        return;
      }
      box.hidden = false;
      input.setAttribute("aria-expanded", "true");
      listEl = el("div", "cmd-list");
      listEl.setAttribute("role", "presentation");
      for (var i = 0; i < found.length; i++) {
        var c = found[i];
        if (c.close && (i === 0 || !found[i - 1].close)) {
          var div = el("div", "cmd-div", "close matches");
          div.setAttribute("role", "separator");
          listEl.appendChild(div);
        }
        var row = el("div", "cmd-row" + (c.saved ? " saved" : ""));
        row.id = "cmd-opt-" + i;
        row.setAttribute("role", "option");
        row.appendChild(el("span", "cmd-name", c.name));
        row.appendChild(el("span", "cmd-help", splitHelp(c.help)[0]));
        if (c.saved) row.appendChild(el("span", "cmd-tag", "saved"));
        (function (k) {
          row.addEventListener("mousedown", function (ev) { ev.preventDefault(); });
          // mousemove, not mouseenter: a list scrolling under a still pointer keeps the keyboard's highlight
          row.addEventListener("mousemove", function (ev) {
            var at = ev.screenX + "," + ev.screenY;
            if (at === lastMove) return;
            lastMove = at;
            if (sel !== k) select(k, false);
          });
          row.addEventListener("click", function () { take(k, true); });
        })(i);
        rows.push(row);
        listEl.appendChild(row);
      }
      var foot = el("div", "cmd-foot");
      foot.setAttribute("aria-hidden", "true");
      usageEl = el("span", "cmd-usage");
      keysEl = el("span", "cmd-keys");
      foot.appendChild(usageEl);
      foot.appendChild(keysEl);
      foot.addEventListener("mousedown", function (ev) { ev.preventDefault(); });
      listEl.addEventListener("scroll", function () {
        if (box.hidden || !rows[sel]) return;
        var top = listEl.scrollTop, bottom = top + listEl.clientHeight;
        var first = -1, last = -1;
        for (var k = 0; k < rows.length; k++) {
          var row = rows[k];
          if (row.offsetTop >= top - 1 && row.offsetTop + row.offsetHeight <= bottom + 1) {
            if (first < 0) first = k;
            last = k;
          }
        }
        if (first < 0) return;
        if (rows[sel].offsetTop < top - 1) select(first, false);
        else if (rows[sel].offsetTop + rows[sel].offsetHeight > bottom + 1) select(last, false);
      });
      box.appendChild(listEl);
      box.appendChild(foot);
      fit();
      select(Math.min(sel, found.length - 1), true);
    }

    // fit caps the list so the box stays between the page header and the input, and scrolls
    // when it is longer than that.
    function fit() {
      var rowH = rows[0].offsetHeight || 21;
      var head = document.querySelector("header.top");
      var limit = head ? head.getBoundingClientRect().bottom : 0;
      var anchor = (box.offsetParent || box.parentElement).getBoundingClientRect();
      var foot = box.lastChild.offsetHeight || rowH;
      var room = anchor.top - limit - 12 - foot - 8;
      listEl.style.maxHeight = Math.max(rowH * 2, Math.min(rowH * MAX_SUGGEST + 6, room)) + "px";
    }

    // keepInView scrolls the list so row shows, with the divider when it sits just above it.
    function keepInView(row) {
      var top = row.offsetTop, bottom = top + row.offsetHeight;
      var prev = row.previousElementSibling;
      if (prev && prev.classList.contains("cmd-div")) top = prev.offsetTop;
      if (row === rows[0]) top = 0;
      if (top < listEl.scrollTop) listEl.scrollTop = top;
      else if (bottom > listEl.scrollTop + listEl.clientHeight) listEl.scrollTop = bottom - listEl.clientHeight + (row === rows[rows.length - 1] ? 6 : 0);
    }

    function select(i, scroll) {
      sel = i;
      for (var k = 0; k < rows.length; k++) {
        rows[k].classList.toggle("sel", k === sel);
        rows[k].setAttribute("aria-selected", String(k === sel));
      }
      if (!rows[sel]) return;
      input.setAttribute("aria-activedescendant", rows[sel].id);
      if (scroll) keepInView(rows[sel]);
      usageEl.textContent = hintFor(found[sel]);
      var keys = "↑↓ · tab · enter · esc";
      if (listEl.scrollHeight > listEl.clientHeight + 1) {
        var lineH = rows[0].offsetHeight || 21;
        var hidden = listEl.children.length - Math.floor(listEl.clientHeight / lineH);
        if (hidden > 0) keys = hidden + " more · " + keys;
      }
      keysEl.textContent = keys;
    }

    // take is tab or enter on row i (a click is enter). Enter runs it; tab writes "name " so words
    // can follow, except for a command that opens a picker, which tab runs too.
    function take(i, enter) {
      var c = found[i];
      if (!c) return;
      if (enter || PICKERS[c.name]) {
        found = []; sel = 0; paint();
        setValue(input, "");
        input.focus();
        run(c.name);
        return;
      }
      setValue(input, c.name + " ");
    }

    function refresh() {
      var text = valueOf(input);
      if (text !== typed) { typed = text; sel = 0; closed = false; }
      found = match(text);
      if (sel >= found.length) sel = 0;
      paint();
      if (text === "/") load(true).then(function () { if (valueOf(input) === text) { found = match(text); paint(); } });
    }

    input.setAttribute("aria-controls", box.id || "cmd-suggest");
    input.setAttribute("aria-autocomplete", "list");
    input.addEventListener("input", refresh);
    input.addEventListener("blur", function () { setTimeout(function () { if (document.activeElement !== input) hide(); }, 120); });
    input.addEventListener("focus", refresh);
    window.addEventListener("resize", function () { if (!box.hidden && rows.length) { fit(); select(sel, true); } });
    // capture: the list handles its keys before the chat input's own handler sees them
    input.addEventListener("keydown", function (ev) {
      if (ev.isComposing) return;
      var text = valueOf(input);
      var plain = !ev.shiftKey && !ev.ctrlKey && !ev.altKey && !ev.metaKey;
      var shown = open() && !box.hidden;
      var done = function () { ev.preventDefault(); ev.stopImmediatePropagation(); };
      if (shown && plain && (ev.key === "ArrowDown" || ev.key === "ArrowUp")) {
        done();
        var n = found.length;
        select(ev.key === "ArrowDown" ? (sel + 1) % n : (sel - 1 + n) % n, true);
        return;
      }
      if (shown && plain && ev.key === "Tab") { done(); take(sel, false); return; }
      if (shown && ev.key === "Escape") { done(); closed = true; paint(); return; }
      if (ev.key === "Enter" && plain && text.charAt(0) === "/") {
        done();
        // A whole command name typed with the highlight untouched runs as typed (an alias like
        // /exit has no row of its own).
        var bare = sel === 0 && / $/.test(text) && found[0] && found[0].option;
        if (shown && !bare && !(sel === 0 && isName(text) && found[0].name !== text)) { take(sel, true); return; }
        found = []; sel = 0; paint();
        run(text);
      }
    }, true);
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

  // Picker
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
           onPick(item): enter or a click; the picker closes first unless it returns true,
           onToggle(item): space on a row marked toggle (the picker stays open),
           onHover(item): the highlight landed on a row (theme preview),
           onClose(): esc, a click outside, or another picker replacing this one; not called after a pick.
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
    root.setAttribute("aria-label", o.title || "choose");
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
    box.setAttribute("aria-label", "Filter " + (o.title || "the list"));
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
      // a list without a filter still keeps its (unseen) box: the box is what takes the keys
      fline.classList.toggle("pick-filter-off", !o.filterable);
      vis = filtered();
      if (sel >= vis.length) sel = vis.length - 1;
      if (sel < 0) sel = 0;
      listEl.textContent = "";
      var nameW = 10, detW = 0;
      vis.forEach(function (it) {
        if (!it.sub) { nameW = Math.max(nameW, String(it.label || "").length); detW = Math.max(detW, String(it.detail || "").length); }
      });
      // the label column first; the detail gets what is left beside the state
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
    // The chat box got the keys (tab, a typed letter), so the picker is done, like a click
    // outside. That way Enter there can never send a chat line under an open picker.
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

  var api = { list: list, all: all, load: load, match: match, rank: rank, attach: attach, runServer: runServer, picker: picker, pickerOpen: pickerOpen, focusPicker: focusPicker, closePicker: closePicker };
  window.PrismCommands = api;
  load();
})();
