package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestRouterTokenInit(t *testing.T) {
	if os.Getenv("PRISM_AUTH_INIT_TEST") == "1" {
		if routerToken != "init-secret" {
			t.Fatal("token not captured")
		}
		if _, exists := os.LookupEnv("PRISM_ROUTER_TOKEN"); exists {
			t.Fatal("token leaked to child environment")
		}
		if _, exists := os.LookupEnv("PRISM_ROUTER_TOKEN_FD"); exists {
			t.Fatal("handover fd environment was not cleared")
		}
		if runtime.GOOS == "linux" {
			initial, err := os.ReadFile("/proc/self/environ")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(initial), "init-secret") || strings.Contains(string(initial), "PRISM_ROUTER_TOKEN=") {
				t.Fatal("token remains readable through /proc/self/environ")
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRouterTokenInit$")
	cmd.Env = append(os.Environ(), "PRISM_AUTH_INIT_TEST=1", "PRISM_ROUTER_TOKEN=init-secret")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestRouterAdminHeaders(t *testing.T) {
	old := routerToken
	routerToken = "test-secret"
	t.Cleanup(func() { routerToken = old })
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-prism-token") != routerToken {
			t.Error("missing router token")
		}
		seen[r.URL.Path] = true
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	var out map[string]any
	if err := loginRound(srv.URL+"/v1", map[string]any{}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := postKey(srv.URL+"/v1", "nv", "secret"); err != nil {
		t.Fatal(err)
	}
	if !seen["/logins"] || !seen["/keys"] {
		t.Fatal("missing admin calls")
	}
	routerToken = ""
	req, _ := http.NewRequest("GET", "http://localhost", nil)
	authorizeRouter(req)
	if strings.Contains(req.Header.Get("x-prism-token"), "secret") {
		t.Fatal("unexpected token")
	}
}
