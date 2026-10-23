package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
