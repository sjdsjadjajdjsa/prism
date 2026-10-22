/* prism slash commands — list mirrors cli/commands.go; runServer talks to cli/serve.go command(). */
(function () {
  var list = [
    { name: "/models", help: "see which free AI models are ready" },
    { name: "/new", help: "start a fresh chat" },
    { name: "/resume", help: "reopen an earlier chat in this folder" },
    { name: "/init", help: "write PRISM.md, the rules prism always follows here" },
    { name: "/skills", help: "playbooks prism can follow · /skills new <name> to write your own" },
    { name: "/web", help: "move this chat to your browser until you close the tab" },
    { name: "/mcp", help: "connected tools (MCP servers) · see docs/MCP.md" },
    { name: "/memory", help: "see what prism remembers · /memory forget" },
    { name: "/usage", help: "free AI used today" },
    { name: "/theme", help: "switch theme, or /theme <name>" },
    { name: "/beam", help: "turn the finish beam on or off" },
    { name: "/quiet", help: "turn Shard's lines on or off" },
    { name: "/help", help: "show these commands · keys: ctrl+o open all steps, ctrl+t theme, esc stop" },
    { name: "/quit", help: "close prism" }
  ];

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

  function render(box, text) {
    var found = match(text);
    box.textContent = "";
    if (!found.length) {
      box.hidden = true;
      return found;
    }
    box.hidden = false;
    for (var i = 0; i < found.length; i++) {
      var row = el("div", "cmd-row");
      row.setAttribute("role", "option");
      row.appendChild(el("span", "cmd-name", found[i].name));
      row.appendChild(el("span", "cmd-help", found[i].help));
      if (i === 0) row.appendChild(el("span", "cmd-hint", "tab"));
      box.appendChild(row);
    }
    return found;
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
  }

  function attach(opts) {
    var input = opts.input;
    var box = opts.suggestBox;
    var run = opts.run;
    box.hidden = true;

    function refresh() { render(box, valueOf(input)); }

    input.addEventListener("input", refresh);
    input.addEventListener("keydown", function (ev) {
      var text = valueOf(input);
      if (ev.key === "Tab") {
        var found = match(text);
        if (found.length) {
          ev.preventDefault();
          setValue(input, found[0].name + " ");
          refresh();
        }
        return;
      }
      if (ev.key === "Escape") {
        box.hidden = true;
        return;
      }
      if (ev.key === "Enter" && !ev.shiftKey && !ev.ctrlKey && !ev.altKey && !ev.metaKey) {
        if (text.charAt(0) === "/") {
          ev.preventDefault();
          box.hidden = true;
          if (typeof run === "function") run(text);
        }
      }
    });
  }

  async function runServer(text) {
    var res = await fetch("/api/command", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ text: text })
    });
    var data = await res.json();
    return data;
  }

  window.PrismCommands = { list: list, match: match, attach: attach, runServer: runServer };
})();
