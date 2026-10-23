package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ddgPage = `<html><body>
<div class="result"><h2><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fgo1.27&amp;rut=abc">Go 1.27 <b>Release</b> Notes</a></h2>
<a class="result__snippet" href="x">The latest Go release, version 1.27, arrives &amp; brings...</a></div>
<div class="result"><a rel="nofollow" class="result__a" href="https://duckduckgo.com/y.js?ad=1">Ad</a><a class="result__snippet">ad</a></div>
<div class="result"><a rel="nofollow" class="result__a" href="https://example.org/two">Second</a><a class="result__snippet">two</a></div>
</body></html>`

func fakeServer(t *testing.T, h http.HandlerFunc) string {
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

func allowLocal(t *testing.T, blocked string) {
	saved := webBlocked
	webBlocked = func(ip net.IP) bool { return ip.String() == blocked }
	t.Cleanup(func() { webBlocked = saved })
}

func setURL(t *testing.T, p *string, v string) {
	saved := *p
	*p = v
	t.Cleanup(func() { *p = saved })
}

func TestWebSearchDuckDuckGo(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("BRAVE_API_KEY", "")
	var form string
	setURL(t, &ddgURL, fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm.Get("q")
		io.WriteString(w, ddgPage)
	}))
	r := runTool(context.Background(), t.TempDir(), "web", `{"search":"go 1.27 release notes"}`)
	if !r.ok || form != "go 1.27 release notes" {
		t.Fatalf("ok=%v form=%q\n%s", r.ok, form, r.output)
	}
	for _, want := range []string{webUntrusted, "1. Go 1.27 Release Notes — https://go.dev/doc/go1.27", "version 1.27, arrives & brings", "2. Second — https://example.org/two", "(DuckDuckGo)"} {
		if !strings.Contains(r.output, want) {
			t.Errorf("output lacks %q:\n%s", want, r.output)
		}
	}
	if strings.Contains(r.output, "y.js") {
		t.Error("ad link kept")
	}
}

func TestWebSearchProviderOrder(t *testing.T) {
	calls := []string{}
	setURL(t, &tavilyURL, fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "tavily")
		w.WriteHeader(500)
	}))
	setURL(t, &braveURL, fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "brave:"+r.Header.Get("x-subscription-token"))
		io.WriteString(w, `{"web":{"results":[{"title":"<b>B</b>","url":"https://b.example","description":"from brave"}]}}`)
	}))
	setURL(t, &ddgURL, fakeServer(t, func(w http.ResponseWriter, r *http.Request) { calls = append(calls, "ddg") }))
	t.Setenv("TAVILY_API_KEY", "tk")
	t.Setenv("BRAVE_API_KEY", "bk")
	r := runTool(context.Background(), t.TempDir(), "web", `{"search":"x"}`)
	if !r.ok || !strings.Contains(r.output, "1. B — https://b.example") || strings.Join(calls, ",") != "tavily,brave:bk" {
		t.Fatalf("calls %v\n%s", calls, r.output)
	}
}

func TestWebReadPage(t *testing.T) {
	allowLocal(t, "127.0.0.2")
	page := `<html><head><title>Docs &amp; Guide</title><script>steal()</script><style>p{}</style></head>
<body><nav>menu</nav><main><h1>Install</h1><p>Run this:</p><pre><code>go install x@latest</code></pre><ul><li>one</li><li>two</li></ul>
<p>` + strings.Repeat("long text ", 1200) + `</p></main><footer>foot</footer></body></html>`
	base := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("content-type", "text/html")
			io.WriteString(w, page)
		case "/json":
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `{"a":1}`)
		case "/img":
			w.Header().Set("content-type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G'})
		}
	})
	r := runTool(context.Background(), t.TempDir(), "web", fmt.Sprintf(`{"read":%q}`, base+"/page"))
	if !r.ok {
		t.Fatal(r.output)
	}
	for _, want := range []string{webUntrusted, "Title: Docs & Guide", "# Install", "```\ngo install x@latest\n```", "- one", "more characters cut"} {
		if !strings.Contains(r.output, want) {
			t.Errorf("read lacks %q", want)
		}
	}
	for _, bad := range []string{"steal()", "menu", "foot", "p{}"} {
		if strings.Contains(r.output, bad) {
			t.Errorf("read kept %q", bad)
		}
	}
	if r := runTool(context.Background(), t.TempDir(), "web", fmt.Sprintf(`{"read":%q}`, base+"/json")); !r.ok || !strings.Contains(r.output, `{"a":1}`) {
		t.Errorf("json: %+v", r)
	}
	if r := runTool(context.Background(), t.TempDir(), "web", fmt.Sprintf(`{"read":%q}`, base+"/img")); r.ok {
		t.Errorf("image read as text: %+v", r)
	}
}

func TestWebReadRefusesLocalAddresses(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret") }))
	defer inner.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(inner.URL, "http://"))
	if r := runTool(context.Background(), t.TempDir(), "web", fmt.Sprintf(`{"read":%q}`, inner.URL)); r.ok || strings.Contains(r.output, "secret") || !strings.Contains(r.output, "local or private") {
		t.Errorf("loopback read: %+v", r)
	}
	allowLocal(t, "127.0.0.2")
	jump := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.2:"+port+"/", http.StatusFound)
	})
	if r := runTool(context.Background(), t.TempDir(), "web", fmt.Sprintf(`{"read":%q}`, jump)); r.ok || strings.Contains(r.output, "secret") {
		t.Errorf("redirect to a private address was followed: %+v", r)
	}
	for _, bad := range []string{"file:///etc/passwd", "ftp://x", "http://user:pw@example.com"} {
		if r := runTool(context.Background(), t.TempDir(), "web", fmt.Sprintf(`{"read":%q}`, bad)); r.ok {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestWebArgs(t *testing.T) {
	for args, want := range map[string]string{`{}`: "exactly one", `{"search":"a","read":"https://x"}`: "exactly one"} {
		if r := runTool(context.Background(), t.TempDir(), "web", args); r.ok || !strings.Contains(r.output, want) {
			t.Errorf("%s: %+v", args, r)
		}
	}
}

func TestDDGUnwrap(t *testing.T) {
	for in, want := range map[string]string{
		"//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com%2Fb%3Fc%3D1&rut=x": "https://a.com/b?c=1",
		"https://example.com/x": "https://example.com/x",
		"javascript:alert(1)":   "",
	} {
		if got := ddgUnwrap(in); got != want {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
}
