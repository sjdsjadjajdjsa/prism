package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func loginTestServer(t *testing.T, signedIn bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/logins":
			state := "false"
			models := 0
			if signedIn {
				state = "true"
				models = 12
			}
			w.Write([]byte(`{"subscriptions":[{"id":"copilot","name":"GitHub Copilot","signedIn":` + state + `,"models":` + fmt.Sprint(models) + `}]}`))
		case r.Method == "POST" && r.URL.Path == "/logins":
			if r.URL.Query().Get("x") == "" && strings.Contains(r.Header.Get("content-type"), "json") {
				w.Write([]byte(`{"userCode":"ABCD-1234","verificationURI":"https://github.com/login/device","expires":900}`))
				return
			}
			w.Write([]byte(`{"userCode":"ABCD-1234","verificationURI":"https://github.com/login/device","expires":900}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestLoginInputLifecycle(t *testing.T) {
	testEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"subscriptions":[{"id":"grok","pending":false}]}`)
			return
		}
		fmt.Fprint(w, `{"error":{"message":"no sign-in is waiting for grok"}}`)
	}))
	defer srv.Close()
	fresh := func() model {
		a := newTestAgent(t, srv.URL+"/v1", t.TempDir())
		return model{tab: newTab(a), chat: true, ticking: true, start: time.Now()}
	}
	update := func(m model, msg tea.Msg) (model, tea.Cmd) {
		next, cmd := m.Update(msg)
		return next.(model), cmd
	}
	t.Run("typed failure without poll", func(t *testing.T) {
		m := fresh()
		m.input = []rune("/login grok bad")
		m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		m, _ = update(m, cmd())
		if m.loginWaiting != "" {
			t.Fatalf("typed failure captured chat: %q", m.loginWaiting)
		}
	})
	t.Run("terminal paste failure", func(t *testing.T) {
		m, _ := update(fresh(), loginCodeMsg{id: "grok", browser: true, expires: 600})
		m, _ = update(m, pasteLoginCode(srv.URL+"/v1", "grok", "bad")())
		if m.loginWaiting != "" {
			t.Fatal("non-retryable paste failure captured chat")
		}
	})
	t.Run("sign out", func(t *testing.T) {
		m, _ := update(fresh(), loginCodeMsg{id: "grok", browser: true, expires: 600})
		other, _ := update(m, loginOutMsg{id: "copilot"})
		if other.loginWaiting != "grok" {
			t.Fatal("unrelated sign-out stopped waiting")
		}
		m, _ = update(m, loginOutMsg{id: "grok"})
		if m.loginWaiting != "" || m.loginFinished != "grok" {
			t.Fatal("sign-out did not clear and finish the wait")
		}
		m, _ = update(m, loginDoneMsg{id: "grok", err: errors.New("late poll")})
		if strings.Contains(m.entries[len(m.entries)-1].text, "late poll") {
			t.Fatal("late poll displayed after sign-out")
		}
	})
	for _, command := range []string{"/new", "/clear"} {
		t.Run(command, func(t *testing.T) {
			m, _ := update(fresh(), loginCodeMsg{id: "grok", browser: true, expires: 600})
			m.input = []rune(command)
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.loginWaiting != "" {
				t.Fatal("reset chat still captures codes")
			}
		})
	}
	t.Run("tab switch", func(t *testing.T) {
		m := fresh()
		m.tabs = []*tab{m.tab, newTab(m.agent)}
		m, _ = update(m, loginCodeMsg{id: "grok", browser: true, expires: 600})
		m, _ = update(m, tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt})
		if m.loginWaiting != "" {
			t.Fatal("other tab captures codes")
		}
	})
	t.Run("escape preserves draft and cancels turn without hiding login success", func(t *testing.T) {
		m, _ := update(fresh(), loginCodeMsg{id: "grok", browser: true, expires: 600})
		cancelled := false
		m.cancel = func() { cancelled = true }
		m.input = []rune("draft")
		m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
		if !cancelled || string(m.input) != "draft" || m.loginWaiting != "" {
			t.Fatal("escape swallowed turn cancellation or draft")
		}
		m, _ = update(m, loginDoneMsg{id: "grok", models: 2})
		if !strings.Contains(strings.Join(m.entries[len(m.entries)-1].lines, "\n"), "signed in to grok") {
			t.Fatal("escape hid eventual sign-in success")
		}
	})
}

func TestBrowserPollStopsAfterSignOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"subscriptions":[{"id":"grok","pending":false,"signedIn":false}]}`)
	}))
	defer srv.Close()
	msg := pollLogin(srv.URL+"/v1", "grok", 3, 4, true)().(loginDoneMsg)
	if msg.err == nil || msg.err.Error() != "the sign-in was cancelled" {
		t.Fatalf("result: %+v", msg)
	}
}

func TestSubmitLoginCodeAndTypedCommand(t *testing.T) {
	testEnv(t)
	for _, fail := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				fmt.Fprint(w, `{"subscriptions":[{"id":"grok","pending":true}]}`)
				return
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.Method != "POST" || r.URL.Path != "/logins" || body["id"] != "grok" || body["code"] != "the-code" {
				t.Errorf("request: %s %s %+v", r.Method, r.URL.Path, body)
			}
			if fail {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":{"message":"that code did not work; copy it again"}}`)
				return
			}
			fmt.Fprint(w, `{"ok":true,"models":["a","b"],"note":"ready"}`)
		}))
		models, note, err := submitLoginCode(srv.URL+"/v1", "grok", "the-code")
		if fail {
			if err == nil || err.Error() != "that code did not work; copy it again" {
				t.Fatalf("error: %v", err)
			}
		} else if err != nil || models != 2 || note != "ready" {
			t.Fatalf("result: %d %q %v", models, note, err)
		}
		a := newTestAgent(t, srv.URL+"/v1", t.TempDir())
		m := model{tab: &tab{agent: a}, loginFinished: "grok"}
		cmd := m.loginCommand([]string{"/login", "grok", "the-code"})
		if m.loginFinished != "" {
			t.Fatal("typed retry still marked finished")
		}
		if cmd == nil {
			t.Fatal("typed code did not submit")
		}
		msg := cmd().(loginDoneMsg)
		if (msg.err != nil) != fail || (!fail && msg.models != 2) {
			t.Fatalf("typed code: %+v", msg)
		}
		// The main chat input becomes the paste input while a browser login waits.
		m.chat = true
		next, _ := m.update(loginCodeMsg{id: "grok", url: "https://auth.test", browser: true, expires: 600})
		m = next.(model)
		if m.loginWaiting != "grok" || !strings.Contains(strings.Join(m.entries[len(m.entries)-1].lines, "\n"), loginPastePrompt) {
			t.Fatal("missing paste prompt")
		}
		m.input = []rune("the-code")
		next, cmd = m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(model)
		if len(m.input) != 0 || cmd == nil {
			t.Fatal("main input did not submit the code")
		}
		next, _ = m.update(cmd())
		m = next.(model)
		if fail && m.loginWaiting != "grok" {
			t.Fatal("failed paste stopped waiting")
		}
		if !fail && (m.loginWaiting != "" || !strings.Contains(strings.Join(m.entries[len(m.entries)-1].lines, "\n"), "signed in to grok · 2 models")) {
			t.Fatal("paste completion missing")
		}
		if fail {
			next, _ = m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
			m = next.(model)
			if m.loginWaiting != "" {
				t.Fatal("escape did not cancel the paste input")
			}
		}
		srv.Close()
	}
}

func TestFetchLoginsAndCommand(t *testing.T) {
	testEnv(t)
	srv := loginTestServer(t, true)
	defer srv.Close()
	api := srv.URL + "/v1"

	rows, err := fetchLogins(api)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "copilot" || !rows[0].SignedIn || rows[0].Models != 12 {
		t.Fatalf("rows: %+v", rows)
	}

	a := newTestAgent(t, api, t.TempDir())
	m := model{tab: &tab{agent: a}}
	cmd := m.loginCommand([]string{"/login", "copilot"})
	msg := cmd().(loginCodeMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.code != "ABCD-1234" || msg.url != "https://github.com/login/device" || msg.expires != 900 {
		t.Fatalf("code msg: %+v", msg)
	}

	v := loginsView(rows)
	rendered := renderCmdView(v, 80)
	for _, want := range []string{"subscriptions", "copilot", "signed in · 12 models"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("loginsView missing %q in:\n%s", want, rendered)
		}
	}
	notSigned := loginsView([]loginRow{{ID: "copilot", Name: "GitHub Copilot"}})
	if !strings.Contains(renderCmdView(notSigned, 80), "not signed in") {
		t.Error("unsigned view should say not signed in")
	}
}

func TestCopilotPollModelArray(t *testing.T) {
	for _, body := range []string{
		`{"status":"ok","models":["a","b"]}`,
		`{"status":"ok","models":[],"note":"signed in, but Copilot did not list its models; run /login copilot again"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		msg := pollLogin(srv.URL+"/v1", "copilot", 3, 10, false)().(loginDoneMsg)
		srv.Close()
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		if strings.Contains(body, `"a"`) && msg.models != 2 {
			t.Fatalf("count: %+v", msg)
		}
		if strings.Contains(body, `"note"`) && !strings.Contains(msg.note, "run /login copilot again") {
			t.Fatalf("missing note: %+v", msg)
		}
	}
}

func TestBrowserPollKeepsWaitingWhileFinishing(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Write([]byte(`{"subscriptions":[{"id":"chatgpt","signedIn":false,"finishing":true,"models":0}]}`))
			return
		}
		w.Write([]byte(`{"subscriptions":[{"id":"chatgpt","signedIn":true,"models":2}]}`))
	}))
	defer srv.Close()
	msg := pollLogin(srv.URL+"/v1", "chatgpt", 3, 15, true)().(loginDoneMsg)
	if msg.err != nil || msg.models != 2 || calls.Load() != 2 {
		t.Fatalf("result: %+v, calls: %d", msg, calls.Load())
	}
	rows := []loginRow{{ID: "copilot", SignedIn: true, Models: 12}, {ID: "antigravity", Finishing: true}}
	text := renderCmdView(loginsView(rows), 100)
	if !strings.Contains(text, "signing in…") {
		t.Fatalf("missing finishing state: %s", text)
	}
	var columns []int
	for _, line := range strings.Split(text, "\n") {
		if n := strings.Index(line, "signed in ·"); n >= 0 {
			columns = append(columns, n)
		}
		if n := strings.Index(line, "signing in…"); n >= 0 {
			columns = append(columns, n)
		}
	}
	if len(columns) != 2 || columns[0] != columns[1] {
		t.Fatalf("unaligned: %q", text)
	}
}

func TestBrowserPollWaitsForPendingResignIn(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"subscriptions":[{"id":"chatgpt","signedIn":true,"pending":true,"models":9}]}`)
			return
		}
		fmt.Fprint(w, `{"subscriptions":[{"id":"chatgpt","signedIn":true,"models":2}]}`)
	}))
	defer srv.Close()
	msg := pollLogin(srv.URL+"/v1", "chatgpt", 3, 15, true)().(loginDoneMsg)
	if msg.err != nil || msg.models != 2 || calls.Load() != 2 {
		t.Fatalf("premature completion: %+v, calls: %d", msg, calls.Load())
	}
}

func TestBrowserPollShowsModelSaveError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"subscriptions":[{"id":"chatgpt","signedIn":true,"models":0,"error":"sign-in could not be saved: disk full"}]}`)
	}))
	defer srv.Close()
	msg := pollLogin(srv.URL+"/v1", "chatgpt", 3, 10, true)().(loginDoneMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "disk full") {
		t.Fatalf("save failure hidden as success: %+v", msg)
	}
}

func TestBrowserPollReportsTimeoutForExistingAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"subscriptions":[{"id":"chatgpt","signedIn":true,"models":9,"error":"the sign-in timed out; run /login again"}]}`)
	}))
	defer srv.Close()
	msg := pollLogin(srv.URL+"/v1", "chatgpt", 3, 10, true)().(loginDoneMsg)
	if msg.err == nil || msg.err.Error() != "the sign-in timed out; run /login again" {
		t.Fatalf("timeout hidden as success: %+v", msg)
	}
}

func TestLoginCommandExpiry(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"browser advertised", `{"verificationURI":"https://auth.test","expires":600}`, 600},
		{"browser fallback", `{"verificationURI":"https://auth.test"}`, 600},
		{"device fallback", `{"userCode":"CODE","verificationURI":"https://auth.test"}`, 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			a := newTestAgent(t, srv.URL+"/v1", t.TempDir())
			m := model{tab: &tab{agent: a}}
			msg := m.loginCommand([]string{"/login", "chatgpt"})().(loginCodeMsg)
			if msg.err != nil || msg.expires != tc.want {
				t.Fatalf("expiry: %+v, want %d", msg, tc.want)
			}
		})
	}
}

func TestLoginForbiddenIsActionable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "admin token missing", http.StatusForbidden)
	}))
	defer srv.Close()
	_, getErr := fetchLogins(srv.URL + "/v1")
	var out map[string]any
	postErr := loginRound(srv.URL+"/v1", map[string]any{}, &out)
	for _, err := range []error{getErr, postErr} {
		if err == nil || !strings.Contains(err.Error(), "admin token missing") || !strings.Contains(err.Error(), "restart prism") {
			t.Fatalf("unhelpful forbidden error: %v", err)
		}
	}
}

// loginScreen drives the real TUI message flow against a fake router and
// returns the drawn chat screen (ANSI stripped). pick chooses grok in the
// /login picker; otherwise /login grok is typed.
func loginScreen(t *testing.T, reply string, pick bool) (model, string) {
	t.Helper()
	testEnv(t)
	t.Setenv("PRISM_NO_BROWSER", "1")
	var started atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"subscriptions":[{"id":"chatgpt","name":"ChatGPT"},{"id":"grok","name":"Grok"}]}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["id"] != "grok" {
			t.Errorf("sign-in for %v", body["id"])
		}
		started.Add(1)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(srv.Close)
	a := newTestAgent(t, srv.URL+"/v1", t.TempDir())
	m := model{tab: newTab(a), chat: true, ticking: true, start: time.Now(), w: 110, h: 32}
	up := func(msg tea.Msg) tea.Cmd {
		next, cmd := m.Update(msg)
		m = next.(model)
		return cmd
	}
	if pick {
		m.input = []rune("/login")
		up(up(tea.KeyPressMsg{Code: tea.KeyEnter})())
		if !m.menu.open || len(m.menu.visible()) != 2 {
			t.Fatalf("picker: %+v", m.menu)
		}
		up(tea.KeyPressMsg{Code: tea.KeyDown})
	} else {
		m.input = []rune("/login grok")
	}
	cmd := up(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no sign-in started")
	}
	code, ok := cmd().(loginCodeMsg)
	if !ok || code.err != nil || started.Load() != 1 {
		t.Fatalf("start: %+v", code)
	}
	if up(code) == nil {
		t.Fatal("no poll after the sign-in started")
	}
	return m, regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;;[^\x1b]*\x1b\\\\").ReplaceAllString(m.View().Content, "")
}

// The router answers a browser sign-in with {url} only; the terminal once
// read verificationURI, so the link was blank and nothing looked pasteable.
func TestBrowserLoginScreenShowsLinkAndPasteInput(t *testing.T) {
	link := "https://auth.x.ai/oauth2/auth?client_id=abc&state=s1"
	for _, pick := range []bool{false, true} {
		t.Run(fmt.Sprintf("picker=%v", pick), func(t *testing.T) {
			m, screen := loginScreen(t, `{"url":"`+link+`","expires":600}`, pick)
			if m.loginWaiting != "grok" {
				t.Fatalf("not waiting for a paste: %q", m.loginWaiting)
			}
			for _, want := range []string{"sign in to grok", link, "paste the code or link from the sign-in page here", "› " + loginInputHint} {
				if !strings.Contains(screen, want) {
					t.Errorf("screen missing %q:\n%s", want, screen)
				}
			}
			if strings.Contains(screen, "ask anything") {
				t.Errorf("input still looks like chat:\n%s", screen)
			}
		})
	}
}

func TestDeviceLoginScreenShowsCode(t *testing.T) {
	for _, pick := range []bool{false, true} {
		t.Run(fmt.Sprintf("picker=%v", pick), func(t *testing.T) {
			m, screen := loginScreen(t, `{"userCode":"WXYZ-9876","verificationURI":"https://accounts.x.ai/device","expires":1800,"interval":7}`, pick)
			if m.loginWaiting != "" {
				t.Fatal("device code sign-in captured the chat input")
			}
			for _, want := range []string{"https://accounts.x.ai/device", "type this code on that page", "WXYZ-9876", "ask anything"} {
				if !strings.Contains(screen, want) {
					t.Errorf("screen missing %q:\n%s", want, screen)
				}
			}
		})
	}
}

func TestLoginBrowserKeywordAndMissingLink(t *testing.T) {
	testEnv(t)
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		fmt.Fprint(w, `{"expires":600}`)
	}))
	defer srv.Close()
	m := model{tab: &tab{agent: newTestAgent(t, srv.URL+"/v1", t.TempDir())}}
	msg := m.loginCommand([]string{"/login", "grok", "browser"})().(loginCodeMsg)
	if len(bodies) != 1 || bodies[0]["browser"] != true {
		t.Fatalf("browser keyword not sent: %v", bodies)
	}
	if msg.err == nil || !strings.Contains(msg.err.Error(), "no sign-in link") {
		t.Fatalf("blank link accepted: %+v", msg)
	}
}
