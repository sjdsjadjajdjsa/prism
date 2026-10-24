package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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
