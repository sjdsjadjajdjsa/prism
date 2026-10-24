package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func keysTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	set := map[string]string{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("content-type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/keys":
			nv := ""
			if set["NV_API_KEY"] != "" {
				nv = "sk-...3456"
			}
			ds := ""
			if set["DEEPSEEK_API_KEY"] != "" {
				ds = "sk-...7890"
			}
			w.Write([]byte(`{"free":[{"id":"nv","name":"nv","var":"NV_API_KEY","set":"` + nv + `"}],"presets":[{"id":"deepseek","name":"DeepSeek","var":"DEEPSEEK_API_KEY","paid":true,"keyUrl":"https://platform.deepseek.com/api_keys","set":"` + ds + `"},{"id":"ollama-local","name":"Ollama","keyless":true}]}`))
		case r.Method == "POST" && r.URL.Path == "/keys":
			var body struct {
				ID    string `json:"id"`
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(400)
				return
			}
			if body.ID == "nope" {
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"message":"no provider called \"nope\""}}`))
				return
			}
			if body.ID == "nv" {
				set["NV_API_KEY"] = body.Value
			}
			if body.ID == "deepseek" {
				set["DEEPSEEK_API_KEY"] = body.Value
			}
			if body.Value == "" {
				w.Write([]byte(`{"ok":true,"removed":true}`))
				return
			}
			w.Write([]byte(`{"ok":true,"masked":"sk-...3456","models":2,"note":""}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestFetchKeysAndPostKey(t *testing.T) {
	srv := keysTestServer(t)
	defer srv.Close()
	api := srv.URL + "/v1"

	info, err := fetchKeys(api)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Free) != 1 || info.Free[0].ID != "nv" || info.Free[0].Set != "" {
		t.Fatalf("free rows: %+v", info.Free)
	}
	if len(info.Presets) != 2 || info.Presets[0].ID != "deepseek" || info.Presets[0].KeyURL == "" {
		t.Fatalf("presets: %+v", info.Presets)
	}

	msg, err := postKey(api, "deepseek", "sk-test-123456")
	if err != nil {
		t.Fatal(err)
	}
	if msg.masked != "sk-...3456" || msg.models != 2 {
		t.Fatalf("postKey msg: %+v", msg)
	}

	info, err = fetchKeys(api)
	if err != nil {
		t.Fatal(err)
	}
	if info.Presets[0].Set != "sk-...7890" {
		t.Fatalf("deepseek should be set after postKey: %+v", info.Presets[0])
	}

	msg, err = postKey(api, "deepseek", "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.masked != "" {
		t.Fatalf("remove should return no masked value: %+v", msg)
	}

	if _, err := postKey(api, "nope", "x"); err == nil || !strings.Contains(err.Error(), "no provider called") {
		t.Fatalf("unknown provider error: %v", err)
	}
}

func TestKeysViewAndLines(t *testing.T) {
	info := keysInfo{
		Free: []keyRow{{ID: "nv", Name: "nv", Var: "NV_API_KEY", Set: "sk-...3456"}},
		Presets: []keyRow{
			{ID: "deepseek", Name: "DeepSeek", Var: "DEEPSEEK_API_KEY", Paid: true, KeyURL: "https://platform.deepseek.com/api_keys"},
			{ID: "ollama-local", Name: "Ollama", Keyless: true},
		},
	}
	v := keysView(info)
	rendered := renderCmdView(v, 80)
	for _, want := range []string{"keys", "free", "nv", "sk-...3456", "add one: /key <name> <secret> · names: deepseek"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("keysView missing %q in:\n%s", want, rendered)
		}
	}
	if len(v.blocks) != 1 || len(v.blocks[0].items) != 1 || v.blocks[0].items[0].name != "nv" {
		t.Fatalf("unset keys should appear only in the hint: %+v", v)
	}
	if strings.Contains(rendered, "not set") || strings.Contains(rendered, "platform.deepseek.com") {
		t.Fatalf("unexpected unset-key detail: %s", rendered)
	}
	if strings.Contains(rendered, "ollama") {
		t.Errorf("keyless presets should be hidden:\n%s", rendered)
	}
	info.Presets = append(info.Presets, keyRow{ID: "openai", Paid: true, Set: "sk-...9999"})
	v = keysView(info)
	if len(v.blocks) != 2 || v.blocks[1].title != "paid" || v.blocks[1].items[0].name != "openai" {
		t.Fatalf("paid grouping: %+v", v)
	}
	lines := keyLines(info)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "nv") || !strings.Contains(joined, "deepseek") || strings.Contains(joined, "ollama") {
		t.Errorf("keyLines: %v", lines)
	}
}

func TestKeysForbiddenIsActionable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "admin token missing", http.StatusForbidden)
	}))
	defer srv.Close()
	_, getErr := fetchKeys(srv.URL + "/v1")
	_, postErr := postKey(srv.URL+"/v1", "nv", "secret")
	for _, err := range []error{getErr, postErr} {
		if err == nil || !strings.Contains(err.Error(), "admin token missing") || !strings.Contains(err.Error(), "restart prism") {
			t.Fatalf("unhelpful forbidden error: %v", err)
		}
	}
}
