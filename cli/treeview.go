package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

//go:embed treeview/tree.js treeview/tree.css treeview/page.html
var treeview embed.FS

func treeAsset(name, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := treeview.ReadFile("treeview/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", kind+"; charset=utf-8")
		w.Write(data)
	}
}

// treeHTML is a self-contained page (works from file://) showing the tree snapshot.
func treeHTML(t *projectTree) []byte {
	page, _ := treeview.ReadFile("treeview/page.html")
	js, _ := treeview.ReadFile("treeview/tree.js")
	css, _ := treeview.ReadFile("treeview/tree.css")
	data := t.json()
	// JSON inside <script> must not close the tag.
	safe := strings.NewReplacer("</", "<\\/", "<!--", "<\\!--").Replace(string(data))
	title := filepath.Base(t.root)
	out := strings.NewReplacer("{{TITLE}}", htmlEscape(title), "{{CSS}}", string(css), "{{JS}}", strings.ReplaceAll(string(js), "</script", "<\\/script"), "{{DATA}}", safe).Replace(string(page))
	return []byte(out)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(s)
}

// treeMain is `prism tree`: print the tree, or write it as a page with --html [file] or as --json.
func treeMain(a *Agent, args []string) int {
	if a.mapReady != nil {
		<-a.mapReady
	}
	if a.tree == nil {
		fmt.Fprintln(os.Stderr, "no project folder")
		return 1
	}
	filter, htmlOut, asJSON, watch := "", "", false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--html":
			htmlOut = ".prism/tree.html"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				htmlOut = args[i+1]
				i++
			}
		case "--json":
			asJSON = true
		case "--watch":
			watch = true
		default:
			filter = strings.TrimSpace(filter + " " + args[i])
		}
	}
	switch {
	case asJSON:
		var v any
		json.Unmarshal(a.tree.json(), &v)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", " ")
		enc.Encode(v)
	case htmlOut != "":
		write := func() error {
			if err := os.MkdirAll(filepath.Dir(htmlOut), 0o755); err != nil {
				return err
			}
			return os.WriteFile(htmlOut, treeHTML(a.tree), 0o644)
		}
		if err := write(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("wrote", htmlOut)
		if watch {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			go a.tree.watch(ctx)
			version := a.tree.version
			for {
				if !a.tree.wait(ctx, version, time.Hour) && ctx.Err() != nil {
					return 0
				}
				a.tree.mu.Lock()
				version = a.tree.version
				a.tree.mu.Unlock()
				if err := write(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				fmt.Println("updated", htmlOut, time.Now().Format("15:04:05"))
			}
		}
	default:
		title, lines := treeCommand(a, filter)
		fmt.Println(title)
		for _, l := range lines {
			fmt.Println(l)
		}
	}
	return 0
}
