package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The todo page has the bug Prism shipped in the demo: the Active filter shows nothing.
const todoPage = `<!doctype html><html><head><title>Todos</title></head><body>
<h1>Todos</h1>
<input id="new" placeholder="What needs to be done?">
<button id="add">Add</button>
<nav><a href="#" data-f="all">All</a> <a href="#" data-f="active">Active</a> <a href="#" data-f="done">Completed</a></nav>
<ul id="list"></ul><p id="left"></p>
<script>
let todos = JSON.parse(localStorage.getItem('t') || '[]'), filter = 'all';
const save = () => localStorage.setItem('t', JSON.stringify(todos));
function render() {
  const list = document.getElementById('list'); list.innerHTML = '';
  todos.filter(t => filter === 'all' || (filter === 'active' ? BUG : t.done)).forEach((t, i) => {
    const li = document.createElement('li');
    const box = document.createElement('input'); box.type = 'checkbox'; box.checked = t.done;
    box.onchange = () => { t.done = box.checked; save(); render(); };
    const del = document.createElement('button'); del.textContent = '×'; del.setAttribute('aria-label', 'Delete ' + t.text);
    del.onclick = () => { todos.splice(todos.indexOf(t), 1); save(); render(); };
    li.append(box, ' ' + t.text + ' ', del); list.append(li);
  });
  document.getElementById('left').textContent = todos.filter(t => !t.done).length + ' left';
}
function add() { const v = document.getElementById('new'); if (!v.value.trim()) return; todos.push({text: v.value.trim(), done: false}); v.value = ''; save(); render(); }
document.getElementById('add').onclick = add;
document.getElementById('new').addEventListener('keydown', e => { if (e.key === 'Enter') add(); });
document.querySelectorAll('nav a').forEach(a => a.onclick = e => { e.preventDefault(); filter = a.dataset.f; render(); });
render();
</script></body></html>`

func browserOrSkip(t *testing.T) {
	t.Helper()
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "chrome-headless-shell"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no Chromium on PATH")
}

func browseRun(t *testing.T, page, args string) toolResult {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	return runTool(context.Background(), dir, "browse", args)
}

const todoSteps = `{"file":"index.html","steps":[
 {"type":"Buy milk","into":"textbox"},{"press":"Enter"},
 {"type":"Walk dog","into":"What needs to be done?"},{"click":"button \"Add\""},
 {"expect_count":2,"of":"listitem"},
 {"click":"checkbox \"Buy milk\""},{"expect_checked":"checkbox \"Buy milk\""},{"expect":"1 left"},
 {"click":"Active"},{"expect":"Walk dog"},{"expect_not":"Buy milk"},
 {"reload":true},{"expect":"1 left"}]}`

func TestBrowseCatchesBrokenFilter(t *testing.T) {
	browserOrSkip(t)
	r := browseRun(t, strings.Replace(todoPage, "BUG", "false", 1), todoSteps)
	if r.ok {
		t.Fatalf("the broken Active filter passed:\n%s", r.output)
	}
	for _, want := range []string{`expect "Walk dog" ✗`, `expect no "Buy milk" ✓`, "steps failed (step 10)", "Page after the last step"} {
		if !strings.Contains(r.output, want) {
			t.Errorf("output lacks %q:\n%s", want, r.output)
		}
	}
}

func TestBrowseFixedPagePasses(t *testing.T) {
	browserOrSkip(t)
	r := browseRun(t, strings.Replace(todoPage, "BUG", "!t.done", 1), todoSteps)
	if !r.ok {
		t.Fatalf("fixed page failed:\n%s", r.output)
	}
	for _, want := range []string{"all 13 steps passed", `checkbox "Buy milk" checked`, `listitem "Walk dog" [`, `button "Delete Walk dog"`, ".prism/checks/"} {
		if !strings.Contains(r.output, want) {
			t.Errorf("output lacks %q:\n%s", want, r.output)
		}
	}
}

func TestBrowseReportsMissingTargetAndPageErrors(t *testing.T) {
	browserOrSkip(t)
	page := `<!doctype html><button onclick="nope()">Go</button><script>console.error("boom")</script>`
	r := browseRun(t, page, `{"file":"index.html","steps":[{"click":"Go"},{"click":"button \"Save\""},{"expect":"never runs"}]}`)
	if r.ok {
		t.Fatalf("want failure:\n%s", r.output)
	}
	for _, want := range []string{`nothing matches "button \"Save\""`, `button "Go"`, "Stopped at step 2: the action could not be done, so steps 3-3 did not run", "console.error boom", "nope is not defined"} {
		if !strings.Contains(r.output, want) {
			t.Errorf("output lacks %q:\n%s", want, r.output)
		}
	}
	if r.summary != "2 actions · stopped: step 2" {
		t.Errorf("summary = %q", r.summary)
	}
	if strings.Contains(r.output, "3. ") {
		t.Errorf("step 3 ran after a failed action:\n%s", r.output)
	}
}

func TestBrowseRowTargetsAndSameNames(t *testing.T) {
	browserOrSkip(t)
	page := `<!doctype html><ul>
<li><input type="checkbox" aria-label="Mark as completed"> Buy milk <button aria-label="Remove">×</button></li>
<li><input type="checkbox" aria-label="Mark as completed"> Walk dog <button>×</button></li></ul>
<p><span id="n">2</span> items left</p>
<button>Sign in</button>
<script>document.querySelectorAll('input').forEach(b=>b.onchange=()=>{document.getElementById('n').textContent=document.querySelectorAll('input:not(:checked)').length})</script>`
	r := browseRun(t, page, `{"file":"index.html","steps":[{"click":"checkbox in \"Walk dog\""},{"expect_checked":"checkbox in \"Walk dog\""},{"expect_unchecked":"checkbox in \"Buy milk\""},{"expect":"1 items left"},{"click":"× in \"Buy milk\""},{"expect_checked":"checkbox in listitem \"Walk dog\""},{"click":"Sign in"},{"click":"checkbox \"Walk dog\""}]}`)
	for _, want := range []string{
		`1. click checkbox in "Walk dog" ✓ (checkbox "Mark as completed" [e`,
		"2. expect checked", "3. expect unchecked", `listitem "Buy milk"`, `text "1 items left"`,
		`5. click × in "Buy milk" ✓ (button "Remove"`, `6. expect checked checkbox in listitem "Walk dog" ✓`,
		`7. click Sign in ✓ (button "Sign in"`, "steps failed (step 8)",
		`To pick the one in a row, write checkbox in "row text"`,
	} {
		if !strings.Contains(r.output, want) {
			t.Errorf("output lacks %q:\n%s", want, r.output)
		}
	}
	if strings.Count(r.output, "✓") != 7 {
		t.Errorf("want steps 1-7 to pass and 8 to fail:\n%s", r.output)
	}
}

func TestBrowseMissingRoleHint(t *testing.T) {
	browserOrSkip(t)
	r := browseRun(t, `<!doctype html><ul><li><span class="box" onclick="this.textContent='x'">o</span> Buy milk</li></ul>`, `{"file":"index.html","steps":[{"click":"checkbox in \"Buy milk\""}]}`)
	for _, want := range []string{"There is no checkbox on the page", "Each browse call starts with an empty browser"} {
		if !strings.Contains(r.output, want) {
			t.Errorf("output lacks %q:\n%s", want, r.output)
		}
	}
}

func TestBrowseArgumentErrors(t *testing.T) {
	for _, c := range []struct{ args, want string }{
		{`{}`, "exactly one of url or file"},
		{`{"file":"a.html","url":"http://localhost"}`, "exactly one of url or file"},
		{`{"file":"index.html","steps":[{}]}`, "step 1 has no action"},
		{`not json`, "not valid JSON"},
		{`{"file":"../x.html"}`, "outside the project"},
	} {
		r := runTool(context.Background(), t.TempDir(), "browse", c.args)
		if r.ok || !strings.Contains(r.output, c.want) {
			t.Errorf("%s: got %+v, want error containing %q", c.args, r, c.want)
		}
	}
}

func TestBrowseHistorySummary(t *testing.T) {
	call := func(id string) Message {
		return Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Type: "function", Function: FunctionCall{Name: "browse", Arguments: `{"file":"index.html"}`}}}}
	}
	msgs := []Message{
		call("a"), {Role: "tool", ToolCallID: "a", Content: "Opened index.html (1280x800, empty browser).\n1. click Add ✓\nPage now:\nbutton \"Add\" [e1]\nRESULT: all 1 steps passed."},
		call("b"), {Role: "tool", ToolCallID: "b", Content: "Opened index.html (1280x800, empty browser).\n1. expect \"x\" ✗ this exact text is not on the page\nRESULT: 1 of 1 steps failed. Fix the page and run browse again."},
		call("c"), {Role: "tool", ToolCallID: "c", Content: "Opened index.html (1280x800, empty browser).\nPage now:\nbutton \"Add\" [e1]\nNo steps given, so nothing was checked yet."},
		call("d"), {Role: "tool", ToolCallID: "d", Content: "Error: provide exactly one of url or file"},
	}
	want := []struct {
		ok      bool
		summary string
	}{{true, "all 1 steps passed"}, {false, "1 of 1 steps failed"}, {true, "page outline"}, {false, "provide exactly one of url or file"}}
	got := entriesFrom(msgs)
	if len(got) != len(want) {
		t.Fatalf("%d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].res.ok != w.ok || got[i].res.summary != w.summary {
			t.Errorf("entry %d = ok %v %q, want ok %v %q", i, got[i].res.ok, got[i].res.summary, w.ok, w.summary)
		}
	}
}
