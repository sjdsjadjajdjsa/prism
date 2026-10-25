package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Paths are project-relative with "/".
type treeNode struct {
	Path       string   `json:"path"`
	Dir        bool     `json:"dir"`
	Size       int64    `json:"size,omitempty"`
	Lang       string   `json:"lang,omitempty"`
	Git        string   `json:"git,omitempty"`
	Agent      string   `json:"agent,omitempty"`
	Rank       float64  `json:"rank,omitempty"`
	Symbols    []string `json:"symbols,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	ImportedBy []string `json:"importedBy,omitempty"`
	Tab        int      `json:"tab,omitempty"`
	TabName    string   `json:"tabName,omitempty"`
	mod        time.Time
	rawImports []string
}

// projectTree is a live map of the project: files (respecting .gitignore), git status,
// top symbols, local imports and a rank. scan builds it once, then refresh or a watcher
// keeps it current.
type projectTree struct {
	mu      sync.Mutex
	root    string
	nodes   map[string]*treeNode
	ignore  *ignoreSet
	module  string
	version int
	changed chan struct{}
	agent   func() map[string]string
	// claims returns (tabID, tabName, true) when path is claimed by a tab, or (0,"",false).
	// nil means no multi-tab claims are available.
	claims    func(path string) (tab int, name string, ok bool)
	truncated bool
	watchOnce sync.Once
	kids      map[string][]*treeNode
	counts    map[string]int
}

const (
	treeMaxFiles   = 20000
	treeMaxParse   = 256 << 10
	treeMaxSymbols = 12
)

var treeSkipDirs = map[string]bool{".git": true, "node_modules": true, "__pycache__": true, ".venv": true, "venv": true,
	".mypy_cache": true, ".pytest_cache": true, ".next": true, ".cache": true, "dist": true, "build": true, "target": true,
	"vendor": true, ".idea": true, ".vscode": true, ".prism": true, "coverage": true, ".tox": true, ".gradle": true}

func newProjectTree(root string) *projectTree {
	return &projectTree{root: root, nodes: map[string]*treeNode{}, changed: make(chan struct{})}
}

func (t *projectTree) scan() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodes = map[string]*treeNode{}
	t.ignore = &ignoreSet{}
	t.truncated = false
	if data, err := os.ReadFile(filepath.Join(t.root, "go.mod")); err == nil {
		if m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindSubmatch(data); m != nil {
			t.module = string(m[1])
		}
	}
	files := 0
	filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == t.root {
			if d != nil && d.IsDir() && p == t.root {
				t.ignore.load(t.root, "")
			}
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, t.root+string(filepath.Separator)))
		if t.skip(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			t.nodes[rel] = &treeNode{Path: rel, Dir: true}
			t.ignore.load(p, rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if files++; files > treeMaxFiles {
			t.truncated = true
			return filepath.SkipAll
		}
		t.nodes[rel] = t.readFile(rel)
		return nil
	})
	t.gitStatus()
	t.link()
	t.version++
}

func (t *projectTree) skip(rel string, dir bool) bool {
	name := path.Base(rel)
	if dir && treeSkipDirs[name] {
		return true
	}
	if name == ".DS_Store" || strings.HasSuffix(name, ".pyc") || name == ".env" || strings.HasPrefix(name, ".env.") {
		return true
	}
	return t.ignore.match(rel, dir)
}

func (t *projectTree) readFile(rel string) *treeNode {
	n := &treeNode{Path: rel, Lang: langOf(rel)}
	info, err := os.Stat(filepath.Join(t.root, filepath.FromSlash(rel)))
	if err != nil {
		return n
	}
	n.Size, n.mod = info.Size(), info.ModTime()
	if n.Lang == "" || n.Size > treeMaxParse {
		return n
	}
	data, err := os.ReadFile(filepath.Join(t.root, filepath.FromSlash(rel)))
	if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return n
	}
	n.Symbols, n.rawImports = parseSource(n.Lang, data)
	return n
}

func (t *projectTree) refresh(paths []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, rel := range paths {
		rel = strings.Trim(filepath.ToSlash(rel), "/")
		if rel == "" || rel == "." {
			continue
		}
		full := filepath.Join(t.root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			for p := range t.nodes {
				if p == rel || strings.HasPrefix(p, rel+"/") {
					delete(t.nodes, p)
				}
			}
			continue
		}
		if t.skip(rel, info.IsDir()) {
			continue
		}
		for dir := path.Dir(rel); dir != "." && t.nodes[dir] == nil; dir = path.Dir(dir) {
			t.nodes[dir] = &treeNode{Path: dir, Dir: true}
		}
		if info.IsDir() {
			t.nodes[rel] = &treeNode{Path: rel, Dir: true}
			filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
				if err != nil || p == full {
					return nil
				}
				r := filepath.ToSlash(strings.TrimPrefix(p, t.root+string(filepath.Separator)))
				if t.skip(r, d.IsDir()) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					t.nodes[r] = &treeNode{Path: r, Dir: true}
				} else if d.Type().IsRegular() {
					t.nodes[r] = t.readFile(r)
				}
				return nil
			})
			continue
		}
		if info.Mode().IsRegular() {
			if old := t.nodes[rel]; old != nil && old.mod.Equal(info.ModTime()) && old.Size == info.Size() {
				continue
			}
			t.nodes[rel] = t.readFile(rel)
		}
	}
	t.gitStatus()
	t.link()
	t.version++
	close(t.changed)
	t.changed = make(chan struct{})
}

// gitStatus marks changed files. Call with t.mu held.
func (t *projectTree) gitStatus() {
	for _, n := range t.nodes {
		n.Git = ""
	}
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	git := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "git", append([]string{"-c", "safe.directory=*", "-C", t.root}, args...)...).Output()
	}
	prefix, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return
	}
	out, err := git("status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
	if err != nil {
		return
	}
	pre := strings.TrimSpace(string(prefix))
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		code, file := e[:2], strings.TrimPrefix(e[3:], pre)
		if code[0] == 'R' || code[0] == 'C' {
			i++
		}
		mark := "M"
		switch {
		case code == "??":
			mark = "?"
		case code[0] == 'A':
			mark = "A"
		case code[0] == 'D' || code[1] == 'D':
			mark = "D"
		}
		if n := t.nodes[file]; n != nil {
			n.Git = mark
		} else if mark == "D" {
			t.nodes[file] = &treeNode{Path: file, Git: "D", Lang: langOf(file)}
		}
	}
}

// link resolves local imports, fills importedBy and computes a PageRank-style rank. Call with t.mu held.
func (t *projectTree) link() {
	t.kids, t.counts = nil, nil
	byDir := map[string][]string{}
	for p, n := range t.nodes {
		if !n.Dir && n.Lang == "go" {
			byDir[path.Dir(p)] = append(byDir[path.Dir(p)], p)
		}
		// [[wiki links]] name a note without its folder, like Obsidian.
		if !n.Dir && n.Lang == "md" {
			key := "[[" + strings.ToLower(strings.TrimSuffix(path.Base(p), ".md"))
			byDir[key] = append(byDir[key], p)
		}
	}
	for _, n := range t.nodes {
		n.Imports, n.ImportedBy, n.Rank = nil, nil, 0
	}
	keys := make([]string, 0, len(t.nodes))
	for p := range t.nodes {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	edges := 0
	for _, p := range keys {
		n := t.nodes[p]
		seen := map[string]bool{}
		for _, raw := range n.rawImports {
			for _, target := range t.resolve(p, n.Lang, raw, byDir) {
				if target != p && !seen[target] && t.nodes[target] != nil {
					seen[target] = true
					n.Imports = append(n.Imports, target)
				}
			}
		}
		sort.Strings(n.Imports)
		for _, target := range n.Imports {
			t.nodes[target].ImportedBy = append(t.nodes[target].ImportedBy, p)
			edges++
		}
	}
	if edges == 0 {
		return
	}
	var files []*treeNode
	for _, p := range keys {
		if n := t.nodes[p]; !n.Dir {
			files = append(files, n)
		}
	}
	rank := map[*treeNode]float64{}
	for _, n := range files {
		rank[n] = 1
	}
	for range 20 {
		next := map[*treeNode]float64{}
		for _, n := range files {
			next[n] = 0.15
		}
		for _, n := range files {
			if len(n.Imports) == 0 {
				continue
			}
			share := 0.85 * rank[n] / float64(len(n.Imports))
			for _, target := range n.Imports {
				next[t.nodes[target]] += share
			}
		}
		rank = next
	}
	top := 0.0
	for _, r := range rank {
		top = math.Max(top, r)
	}
	for n, r := range rank {
		if len(n.ImportedBy) > 0 {
			n.Rank = math.Round(r/top*100) / 100
		}
	}
}

var jsExts = []string{"", ".js", ".ts", ".tsx", ".jsx", ".mjs", ".cjs", "/index.js", "/index.ts", "/index.tsx", "/index.jsx"}

func (t *projectTree) resolve(from, lang, raw string, byDir map[string][]string) []string {
	dir := path.Dir(from)
	switch lang {
	case "py":
		mod := raw
		base := ""
		if strings.HasPrefix(mod, ".") {
			trimmed := strings.TrimLeft(mod, ".")
			up := len(mod) - len(trimmed) - 1
			base = dir
			for range up {
				base = path.Dir(base)
			}
			mod = trimmed
		}
		rel := strings.ReplaceAll(mod, ".", "/")
		var out []string
		for _, start := range []string{base, dir, ""} {
			for _, cand := range []string{rel + ".py", rel + "/__init__.py"} {
				p := path.Clean(path.Join(start, cand))
				if t.nodes[p] != nil {
					return append(out, p)
				}
			}
		}
		return out
	case "js", "ts", "html", "css":
		if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "data:") {
			return nil
		}
		base := path.Join(dir, raw)
		if strings.HasPrefix(raw, "/") {
			base = strings.TrimPrefix(raw, "/")
		}
		for _, ext := range jsExts {
			if p := path.Clean(base + ext); t.nodes[p] != nil && !t.nodes[p].Dir {
				return []string{p}
			}
		}
	case "md":
		if name, ok := strings.CutPrefix(raw, "[["); ok {
			return byDir["[["+strings.ToLower(strings.TrimSuffix(name, ".md"))]
		}
		if strings.Contains(raw, "://") || strings.HasPrefix(raw, "mailto:") {
			return nil
		}
		if unescaped, err := url.PathUnescape(raw); err == nil {
			raw = unescaped
		}
		if p := path.Clean(path.Join(dir, raw)); t.nodes[p] != nil && !t.nodes[p].Dir {
			return []string{p}
		}
	case "go":
		if t.module != "" && (raw == t.module || strings.HasPrefix(raw, t.module+"/")) {
			return byDir[strings.TrimPrefix(strings.TrimPrefix(raw, t.module), "/")]
		}
	}
	return nil
}

// snapshot returns a JSON-ready copy with the agent's activity merged in.
func (t *projectTree) snapshot() map[string]any {
	var agent map[string]string
	if t.agent != nil {
		agent = t.agent()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	nodes := make([]treeNode, 0, len(t.nodes))
	stats := map[string]int64{}
	for _, n := range t.nodes {
		c := *n
		c.Agent = agent[c.Path]
		if t.claims != nil {
			if tabID, tabName, ok := t.claims(c.Path); ok {
				c.Tab, c.TabName = tabID, tabName
			}
		}
		nodes = append(nodes, c)
		if c.Dir {
			stats["dirs"]++
			continue
		}
		stats["files"]++
		stats["bytes"] += c.Size
		switch c.Git {
		case "M", "D":
			stats["modified"]++
		case "A":
			stats["added"]++
		case "?":
			stats["untracked"]++
		}
		if c.Agent != "" {
			stats["touched"]++
		}
		if c.Tab != 0 {
			stats["claimed"]++
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	return map[string]any{"root": filepath.Base(t.root), "version": t.version, "generated": time.Now().UTC().Format(time.RFC3339),
		"stats": stats, "nodes": nodes, "truncated": t.truncated}
}

func (t *projectTree) startWatching() {
	t.watchOnce.Do(func() {
		go t.watch(context.Background())
	})
}

// poll is the fallback when inotify isn't available.
func (t *projectTree) poll(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if changed := t.changedPaths(); len(changed) > 0 {
			t.refresh(changed)
		}
	}
}

func (t *projectTree) changedPaths() []string {
	seen := map[string]bool{}
	var changed []string
	filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == t.root {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, t.root+string(filepath.Separator)))
		t.mu.Lock()
		skip := t.skip(rel, d.IsDir())
		old := t.nodes[rel]
		t.mu.Unlock()
		if skip {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		seen[rel] = true
		if d.IsDir() {
			if old == nil {
				changed = append(changed, rel)
			}
			return nil
		}
		if info, err := d.Info(); err == nil && (old == nil || !old.mod.Equal(info.ModTime()) || old.Size != info.Size()) {
			changed = append(changed, rel)
		}
		return nil
	})
	t.mu.Lock()
	for p, n := range t.nodes {
		if !seen[p] && n.Git != "D" {
			changed = append(changed, p)
		}
	}
	t.mu.Unlock()
	return changed
}

func (t *projectTree) refreshLater(p string) {
	if p == "" {
		return
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(t.root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return
		}
		p = rel
	}
	go t.refresh([]string{p})
}

func (t *projectTree) json() []byte {
	data, _ := json.Marshal(t.snapshot())
	return data
}

func (t *projectTree) wait(ctx context.Context, version int, timeout time.Duration) bool {
	t.mu.Lock()
	if t.version > version {
		t.mu.Unlock()
		return true
	}
	ch := t.changed
	t.mu.Unlock()
	select {
	case <-ch:
		return true
	case <-ctx.Done():
	case <-time.After(timeout):
	}
	return false
}

var langExts = map[string]string{".go": "go", ".py": "py", ".js": "js", ".mjs": "js", ".cjs": "js", ".jsx": "js", ".ts": "ts", ".tsx": "ts",
	".html": "html", ".htm": "html", ".css": "css", ".md": "md", ".sh": "sh", ".bash": "sh", ".rs": "rs", ".java": "java", ".rb": "rb",
	".json": "json", ".yml": "yaml", ".yaml": "yaml", ".toml": "toml", ".c": "c", ".h": "c", ".cpp": "cpp", ".php": "php", ".swift": "swift", ".kt": "kt"}

func langOf(p string) string {
	if strings.EqualFold(path.Base(p), "dockerfile") {
		return "docker"
	}
	return langExts[strings.ToLower(path.Ext(p))]
}

var (
	symGo   = regexp.MustCompile(`^(func(?: \([^)]*\))? [A-Za-z_]\w*|type [A-Za-z_]\w*(?: (?:struct|interface))?)`)
	symPy   = regexp.MustCompile(`^(?:async )?(def|class) ([A-Za-z_]\w*)`)
	symJS   = regexp.MustCompile(`^(?:export )?(?:default )?(?:async )?(function\*?|class|interface|type|enum) ([A-Za-z_$][\w$]*)|^export (?:const|let|var) ([A-Za-z_$][\w$]*)|^(?:const|let) ([A-Za-z_$][\w$]*) = (?:async )?(?:\([^)]*\)|[A-Za-z_$][\w$]*) =>`)
	symRs   = regexp.MustCompile(`^(?:pub(?:\([^)]*\))? )?(fn|struct|enum|trait|impl|mod) ([A-Za-z_][\w<>]*)`)
	symSh   = regexp.MustCompile(`^(?:function )?([A-Za-z_][\w-]*) ?\(\) ?\{`)
	symMd   = regexp.MustCompile(`^(#{1,2}) (.+)`)
	symHTML = regexp.MustCompile(`(?i)<title>([^<]{1,80})</title>`)
	impPy   = regexp.MustCompile(`^\s*(?:from\s+([.\w]+)\s+import|import\s+([\w.]+))`)
	impJS   = regexp.MustCompile(`(?:import\s+(?:[^'"]*?\s+from\s+)?|export\s+[^'"]*?\s+from\s+|require\(\s*|import\(\s*)['"]([^'"]+)['"]`)
	impHTML = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"'#?]+)`)
	impMd   = regexp.MustCompile(`\]\(([^)\s#?]+)|\[\[([^\]|#]+)`)
	impCSS  = regexp.MustCompile(`@import\s+(?:url\()?['"]?([^'")\s]+)`)
	impGo   = regexp.MustCompile(`^\s*(?:import\s+)?(?:[A-Za-z_.]+\s+)?"([^"]+)"`)
)

// parseSource returns up to treeMaxSymbols top-level symbols and the raw import strings.
func parseSource(lang string, data []byte) (symbols, imports []string) {
	add := func(s string) {
		if len(symbols) < treeMaxSymbols {
			symbols = append(symbols, clipLine(s, 70))
		}
	}
	inGoImport := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch lang {
		case "go":
			if strings.HasPrefix(line, "import (") {
				inGoImport = true
				continue
			}
			if inGoImport {
				if strings.HasPrefix(line, ")") {
					inGoImport = false
				} else if m := impGo.FindStringSubmatch(line); m != nil {
					imports = append(imports, m[1])
				}
				continue
			}
			if strings.HasPrefix(line, "import ") {
				if m := impGo.FindStringSubmatch(line); m != nil {
					imports = append(imports, m[1])
				}
			} else if m := symGo.FindString(line); m != "" {
				add(m)
			}
		case "py":
			if m := symPy.FindStringSubmatch(line); m != nil {
				add(m[1] + " " + m[2])
			} else if m := impPy.FindStringSubmatch(line); m != nil {
				imports = append(imports, m[1]+m[2])
			}
		case "js", "ts":
			if m := symJS.FindStringSubmatch(line); m != nil {
				switch {
				case m[2] != "":
					add(strings.TrimSuffix(m[1], "*") + " " + m[2])
				case m[3] != "":
					add("const " + m[3])
				case m[4] != "":
					add("const " + m[4])
				}
			}
			for _, m := range impJS.FindAllStringSubmatch(line, -1) {
				if strings.HasPrefix(m[1], ".") || strings.HasPrefix(m[1], "/") {
					imports = append(imports, m[1])
				}
			}
		case "rs":
			if m := symRs.FindStringSubmatch(line); m != nil {
				add(m[1] + " " + m[2])
			}
		case "sh":
			if m := symSh.FindStringSubmatch(line); m != nil {
				add(m[1] + "()")
			}
		case "md":
			if m := symMd.FindStringSubmatch(line); m != nil {
				add(m[1] + " " + m[2])
			}
			for _, m := range impMd.FindAllStringSubmatch(line, -1) {
				if m[2] != "" {
					imports = append(imports, "[["+strings.TrimSpace(m[2]))
				} else {
					imports = append(imports, m[1])
				}
			}
		case "html":
			if m := symHTML.FindStringSubmatch(line); m != nil {
				add("title: " + strings.TrimSpace(m[1]))
			}
			for _, m := range impHTML.FindAllStringSubmatch(line, -1) {
				imports = append(imports, m[1])
			}
		case "css":
			if m := impCSS.FindStringSubmatch(line); m != nil {
				imports = append(imports, m[1])
			}
		}
	}
	return symbols, imports
}

type ignoreRule struct {
	base    string
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

type ignoreSet struct{ rules []ignoreRule }

// load reads dir/.gitignore; base is dir relative to the project root ("" for the root).
func (s *ignoreSet) load(dir, base string) {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, " \r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: base}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimSuffix(line, "/")
		}
		anchored := strings.Contains(strings.TrimSuffix(line, "/**"), "/")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		var b strings.Builder
		b.WriteString("^")
		if !anchored {
			b.WriteString("(?:.*/)?")
		}
		for i := 0; i < len(line); i++ {
			switch c := line[i]; c {
			case '*':
				if i+1 < len(line) && line[i+1] == '*' {
					if i+2 < len(line) && line[i+2] == '/' {
						b.WriteString("(?:.*/)?")
						i += 2
					} else {
						b.WriteString(".*")
						i++
					}
				} else {
					b.WriteString("[^/]*")
				}
			case '?':
				b.WriteString("[^/]")
			case '\\':
				if i+1 < len(line) {
					i++
					b.WriteString(regexp.QuoteMeta(string(line[i])))
				}
			case '[':
				if j := strings.IndexByte(line[i:], ']'); j > 0 {
					class := line[i : i+j+1]
					if strings.HasPrefix(class, "[!") {
						class = "[^" + class[2:]
					}
					b.WriteString(class)
					i += j
				} else {
					b.WriteString(`\[`)
				}
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		b.WriteString("(?:/.*)?$")
		if re, err := regexp.Compile(b.String()); err == nil {
			r.re = re
			s.rules = append(s.rules, r)
		}
	}
}

func (s *ignoreSet) match(rel string, dir bool) bool {
	if s == nil {
		return false
	}
	ignored := false
	for _, r := range s.rules {
		p := rel
		if r.base != "" {
			if !strings.HasPrefix(rel, r.base+"/") {
				continue
			}
			p = strings.TrimPrefix(rel, r.base+"/")
		}
		target := p
		if r.dirOnly && !dir {
			// "name/" only matches folders; a file is ignored when one of its folders matches.
			if !strings.Contains(p, "/") {
				continue
			}
			target = path.Dir(p)
		}
		if r.re.MatchString(target) {
			ignored = !r.negate
		}
	}
	return ignored
}

// promptMap is the project map in the system prompt: a folder tree that fits in budget
// characters, with the most depended-on files starred and their top symbols listed.
func (t *projectTree) promptMap(budget int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	files := 0
	for _, n := range t.nodes {
		if !n.Dir {
			files++
		}
	}
	if files == 0 {
		return "\n\nProject files when this chat started: none, the folder is empty."
	}
	ranked := []*treeNode{}
	for _, n := range t.nodes {
		if !n.Dir && n.Rank >= 0.3 && len(n.Symbols) > 0 {
			ranked = append(ranked, n)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Rank > ranked[j].Rank || ranked[i].Rank == ranked[j].Rank && ranked[i].Path < ranked[j].Path
	})
	key := map[string]bool{}
	for i, n := range ranked {
		if i < 6 {
			key[n.Path] = true
		}
	}
	for limit := 40; limit >= 3; limit = limit * 2 / 3 {
		var b strings.Builder
		t.renderDir(&b, "", 0, limit, key, files <= 25)
		if b.Len() <= budget || limit == 3 {
			head := "\n\nProject map when this chat started (★ = many files use it; folders like node_modules and .gitignored files are left out):\n"
			out := b.String()
			if len(out) > budget {
				out = clipUTF8(out, budget) + "\n… (map cut; use list or grep for the rest)"
			}
			return head + strings.TrimRight(out, "\n")
		}
	}
	return ""
}

// index groups nodes by parent folder and counts files under each folder, so the text views
// run in O(n log n) instead of rescanning every node per folder. Call with t.mu held.
func (t *projectTree) index() {
	t.kids, t.counts = map[string][]*treeNode{}, map[string]int{}
	for p, n := range t.nodes {
		parent := path.Dir(p)
		if parent == "." {
			parent = ""
		}
		t.kids[parent] = append(t.kids[parent], n)
		if !n.Dir {
			for d := parent; d != ""; d = parentOf(d) {
				t.counts[d]++
			}
		}
	}
	for _, list := range t.kids {
		sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	}
}

func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

func (t *projectTree) children(dir string) (dirs, files []*treeNode) {
	if t.kids == nil {
		t.index()
	}
	for _, n := range t.kids[dir] {
		if n.Dir {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}
	return
}

func (t *projectTree) countFiles(dir string) int {
	if t.counts == nil {
		t.index()
	}
	return t.counts[dir]
}

// renderDir writes one folder level. Folders with more than limit files are summarised.
func (t *projectTree) renderDir(b *strings.Builder, dir string, depth, limit int, key map[string]bool, symbols bool) {
	dirs, files := t.children(dir)
	pad := strings.Repeat("  ", depth)
	for _, d := range dirs {
		count := t.countFiles(d.Path)
		if count > limit || depth >= 4 {
			fmt.Fprintf(b, "%s%s/ (%d files", pad, path.Base(d.Path), count)
			if top := t.topIn(d.Path, 3); len(top) > 0 {
				b.WriteString("; main: " + strings.Join(top, ", "))
			}
			b.WriteString(")\n")
			continue
		}
		fmt.Fprintf(b, "%s%s/\n", pad, path.Base(d.Path))
		t.renderDir(b, d.Path, depth+1, limit, key, symbols)
	}
	var code, other []*treeNode
	for _, f := range files {
		if f.Lang == "" || f.Lang == "json" && f.Size > 20000 {
			other = append(other, f)
		} else {
			code = append(code, f)
		}
	}
	shown := code
	if len(code) > limit {
		shown = code[:limit]
	}
	for _, f := range shown {
		star := ""
		if key[f.Path] {
			star = " ★"
		}
		fmt.Fprintf(b, "%s%s (%s)%s", pad, path.Base(f.Path), byteSize(f.Size), star)
		if (key[f.Path] || symbols) && len(f.Symbols) > 0 {
			b.WriteString(": " + strings.Join(f.Symbols[:min(4, len(f.Symbols))], ", "))
		}
		b.WriteString("\n")
	}
	if len(code) > len(shown) {
		fmt.Fprintf(b, "%s… %d more files\n", pad, len(code)-len(shown))
	}
	if len(other) > 0 {
		exts := map[string]bool{}
		var list []string
		for _, f := range other {
			e := strings.TrimPrefix(path.Ext(f.Path), ".")
			if e == "" {
				e = path.Base(f.Path)
			}
			if !exts[e] && len(list) < 5 {
				exts[e] = true
				list = append(list, e)
			}
		}
		if len(other) <= 2 {
			for _, f := range other {
				fmt.Fprintf(b, "%s%s (%s)\n", pad, path.Base(f.Path), byteSize(f.Size))
			}
		} else {
			fmt.Fprintf(b, "%s+ %d other files (%s)\n", pad, len(other), strings.Join(list, ", "))
		}
	}
}

func (t *projectTree) topIn(dir string, n int) []string {
	var in []*treeNode
	for p, node := range t.nodes {
		if !node.Dir && strings.HasPrefix(p, dir+"/") && node.Lang != "" && node.Lang != "md" && !strings.Contains(p, "_test.") && !strings.Contains(p, ".test.") {
			in = append(in, node)
		}
	}
	sort.Slice(in, func(i, j int) bool {
		if in[i].Rank != in[j].Rank {
			return in[i].Rank > in[j].Rank
		}
		return in[i].Size > in[j].Size || in[i].Size == in[j].Size && in[i].Path < in[j].Path
	})
	var out []string
	for _, node := range in[:min(n, len(in))] {
		if node.Size == 0 {
			continue
		}
		out = append(out, strings.TrimPrefix(node.Path, dir+"/"))
	}
	return out
}

func tabCircled(n int) string {
	if n >= 1 && n <= 9 {
		return string(rune('\u2460' + n - 1)) // ①…⑨
	}
	return fmt.Sprintf("[%d]", n)
}

func treeCommand(a *Agent, filter string) (string, []string) {
	if a.mapReady != nil {
		<-a.mapReady
	}
	if a.tree == nil {
		return "project files", []string{"no project folder"}
	}
	a.tree.startWatching()
	agent := a.touchedFiles()
	lines := a.tree.asciiTree(filter, agent, 300)
	snap := a.tree.snapshot()["stats"].(map[string]int64)
	head := fmt.Sprintf("%d files · %d changed · %d touched by prism", snap["files"], snap["modified"]+snap["added"]+snap["untracked"], snap["touched"])
	if len(lines) == 0 {
		lines = []string{"nothing matches " + filter}
	}
	legend := "M changed · ? new · ● prism edited · ◦ prism read · ★ many files use it · /tree changed · /tree <text>"
	if snap["claimed"] > 0 {
		legend += " · ①…⑨ tab editing"
	}
	return "project files · " + head, append(lines, "", legend)
}

// asciiTree is the view for /tree and `prism tree`. filter keeps paths containing it
// (any case), or only changed files when it is "changed".
func (t *projectTree) asciiTree(filter string, agent map[string]string, maxLines int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	filter = strings.ToLower(strings.TrimSpace(filter))
	keep := map[string]bool{}
	for p, n := range t.nodes {
		if n.Dir {
			continue
		}
		ok := filter == "" ||
			(filter == "changed" && (n.Git != "" || agent[p] == "edit" || agent[p] == "write")) ||
			(filter == "claimed" && t.claims != nil && func() bool { _, _, ok := t.claims(p); return ok }()) ||
			(filter != "changed" && filter != "claimed" && strings.Contains(strings.ToLower(p), filter))
		if ok {
			for q := p; q != "." && q != ""; q = path.Dir(q) {
				keep[q] = true
			}
		}
	}
	var lines []string
	var walk func(dir, prefix string)
	walk = func(dir, prefix string) {
		dirs, files := t.children(dir)
		all := append(dirs, files...)
		var visible []*treeNode
		for _, n := range all {
			if keep[n.Path] {
				visible = append(visible, n)
			}
		}
		for i, n := range visible {
			if len(lines) >= maxLines {
				return
			}
			branch, next := "├─ ", "│  "
			if i == len(visible)-1 {
				branch, next = "└─ ", "   "
			}
			name := path.Base(n.Path)
			if n.Dir {
				lines = append(lines, prefix+branch+name+"/")
				walk(n.Path, prefix+next)
				continue
			}
			marks := ""
			if n.Git != "" {
				marks += " " + n.Git
			}
			switch agent[n.Path] {
			case "write", "edit":
				marks += " ●"
			case "read":
				marks += " ◦"
			}
			if n.Rank >= 0.5 {
				marks += " ★"
			}
			if t.claims != nil {
				if tabID, _, ok := t.claims(n.Path); ok {
					marks += " " + tabCircled(tabID)
				}
			}
			lines = append(lines, prefix+branch+name+marks)
		}
	}
	walk("", "")
	if len(lines) >= maxLines {
		lines = append(lines, fmt.Sprintf("… more (narrow it: /tree <text> or /tree changed)"))
	}
	return lines
}
