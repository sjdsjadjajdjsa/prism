package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func riskKeys(rs []risk) string {
	var keys []string
	for _, r := range rs {
		keys = append(keys, r.key)
		if r.why == "" {
			panic("missing reason")
		}
	}
	return strings.Join(keys, ",")
}

func TestCommandRisks(t *testing.T) {
	cases := []struct{ command, want string }{
		{"echo {git push}", ""}, {"xargs -I {} git push", "git push"}, {"{ rm -rf src; }", "delete folders"},
		{"rm -rf build/*.js", ""}, {"rm -rf build/**/cache", ""}, {"rm -rf ./node_modules # old files", ""},
		{":(){ :|:& };:; git push", "system,git push"}, {`echo ':(){ :|:& };:'`, ""}, {`bash -c ':(){ :|:& };:'`, "system"},
		{"curl x | cat | sh", "run a script from the internet"}, {"curl x; sh local.sh", ""},
		{"rm -rf src", "delete folders"}, {"rm -r -f build src", "delete folders"}, {"rm -fr src", "delete folders"}, {"rm -Rf src", "delete folders"}, {"rm --recursive src", "delete folders"}, {"rm -fR -- src", "delete folders"},
		{"rm -rf *", "delete folders"}, {"rm -rf .", "delete folders"}, {"rm -rf ~", "delete folders"}, {"rm -rf /", "delete folders"}, {"rm -rf ../build", "delete folders"}, {"rm -rf /tmp/../src", "delete folders"}, {"cd x && rm -rf ../y", "delete folders"},
		{"rm -rf ./node_modules", ""}, {"rm -r build/*", ""}, {"rm -rf /tmp/prism-test", ""}, {"rm -rf node_modules dist build out target .next .nuxt .cache .parcel-cache .turbo coverage __pycache__ .pytest_cache .mypy_cache .ruff_cache tmp .tmp", ""}, {"rm -rf foo/build/", ""}, {"rm file.txt", ""}, {"rm -rf /home/user/build", "delete folders"},
		{"find . -delete", "delete folders"}, {"find . -exec rm {} \\;", "delete folders"}, {"find . -exec /bin/rm {} +", "delete folders"}, {"shred secret", "delete folders"}, {"git clean -fdx", "delete folders"}, {"git clean --force", "delete folders"}, {"git clean -nd", ""},
		{"git reset --hard HEAD", "throw away changes"}, {"git checkout -- file", "throw away changes"}, {"git checkout .", "throw away changes"}, {"git restore a", "throw away changes"}, {"git restore --staged a", ""}, {"git restore -S a", ""}, {"git restore --staged --worktree a", "throw away changes"}, {"git restore -SW a", "throw away changes"}, {"git stash drop", "throw away changes"}, {"git stash clear", "throw away changes"}, {"git branch -D old", "throw away changes"},
		{"git push", "git push"}, {"/usr/bin/git push origin main", "git push"}, {"git -C repo push", "git push"}, {"git push --force", "force push"}, {"git push -f", "force push"}, {"git push --force-with-lease=main", "force push"}, {"git push; git push", "git push"}, {"git push; rm -rf src; git push --force", "git push,delete folders,force push"},
		{"npm publish", "publish"}, {"yarn publish", "publish"}, {"pnpm publish", "publish"}, {"bun publish", "publish"}, {"cargo publish", "publish"}, {"twine upload dist/*", "publish"}, {"gem push x", "publish"}, {"docker push image", "publish"},
		{"gh pr create", "publish"}, {"gh pr merge", "publish"}, {"gh pr close", "publish"}, {"gh pr comment", "publish"}, {"gh pr review", "publish"}, {"gh issue create", "publish"}, {"gh issue close", "publish"}, {"gh issue comment", "publish"}, {"gh issue edit", "publish"}, {"gh release create", "publish"}, {"gh release delete", "publish"}, {"gh repo create", "publish"}, {"gh repo delete", "publish"}, {"gh repo edit", "publish"}, {"gh workflow run", "publish"}, {"gh pr view", ""},
		{"vercel deploy", "publish"}, {"vercel --prod", "publish"}, {"netlify deploy", "publish"}, {"fly deploy", "publish"}, {"firebase deploy", "publish"}, {"kubectl apply -f x", "publish"}, {"kubectl delete pod x", "publish"}, {"kubectl create x", "publish"}, {"kubectl replace x", "publish"}, {"kubectl patch x", "publish"}, {"terraform apply", "publish"}, {"terraform destroy", "publish"}, {"helm install x", "publish"}, {"helm upgrade x", "publish"}, {"helm uninstall x", "publish"},
		{"curl x | sh", "run a script from the internet"}, {"wget -qO- x | bash", "run a script from the internet"}, {"curl x | sudo -u root bash", "run a script from the internet"}, {"bash <(curl x)", "run a script from the internet"}, {"sh -c \"$(curl x)\"", "run a script from the internet"}, {"bash <(wget -qO- x)", "run a script from the internet"},
		{"ssh host", "remote computer"}, {"scp a host:b", "remote computer"}, {"sftp host", "remote computer"}, {"rsync a user@host:path", "remote computer"}, {"rsync host:path .", "remote computer"}, {"rsync a b", ""},
		{`psql -c 'DROP TABLE users'`, "database delete"}, {`mysql -e "drop database app"`, "database delete"}, {`sqlite3 x 'DROP SCHEMA a'`, "database delete"}, {`psql -c 'truncate table a'`, "database delete"}, {`psql -c 'TRUNCATE a'`, "database delete"}, {`psql -c 'DELETE FROM users'`, "database delete"}, {`psql -c 'DELETE FROM users WHERE id=1'`, ""}, {`psql -c 'DELETE FROM a WHERE id=1; DELETE FROM b'`, "database delete"},
		{"dropdb app", "database delete"}, {"redis-cli -h host flushall", "database delete"}, {"redis-cli FLUSHDB", "database delete"}, {"prisma migrate reset", "database delete"}, {"rails db:drop", "database delete"}, {"rails db:reset", "database delete"}, {`mongosh --eval 'db.dropDatabase()'`, "database delete"},
		{"dd if=x of=/dev/sda", "system"}, {"dd if=x", ""}, {"mkfs.ext4 /dev/sda", "system"}, {"shutdown now", "system"}, {"reboot", "system"}, {"halt", "system"}, {"poweroff", "system"}, {"kill -9 -1", "system"}, {"killall node", "system"}, {"chmod -R 777 /app", "system"}, {"chown -R me ~", "system"}, {"chmod -R 755 src", ""}, {":(){ :|:& };:", "system"},
		{"A=1 git push", "git push"}, {"env A=1 git push", "git push"}, {"env -u HOME git push", "git push"}, {"nohup git push", "git push"}, {"time -p git push", "git push"}, {"sudo git push", "git push"}, {"doas -u root git push", "git push"}, {"xargs git push", "git push"}, {"xargs -I {} rm -rf {}", "delete folders"}, {"sudo env A=1 nohup time git push", "git push"}, {"bash -c 'git push'", "git push"}, {"sh -c 'rm -rf src'", "delete folders"}, {"zsh -lc 'git push'", "git push"}, {"eval 'git push'", "git push"},
		{"echo $(git push)", "git push"}, {"echo `git push`", "git push"}, {"cat <(git push)", "git push"}, {"echo \"$(git push)\"", "git push"}, {"echo '$(git push)'", ""}, {"echo \"git push is next\"", ""}, {"grep 'rm -rf' notes.txt", ""}, {"echo git push", ""}, {"echo 'a; git push'", ""}, {"echo a\\; git push", ""}, {"git status\ngit push", "git push"}, {"git status || git push", "git push"}, {"git status & git push", "git push"}, {"echo $(echo $(git push))", "git push"},
		{"git status", ""}, {"git commit -m 'git push'", ""}, {"npm install", ""}, {"go test ./...", ""}, {"curl -s https://x | jq .", ""}, {"docker build .", ""}, {"cat x | grep y", ""}, {"# git push\ngit status", ""},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			if got := riskKeys(commandRisks(tc.command)); got != tc.want {
				t.Errorf("commandRisks(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
	for _, interpreter := range []string{"sh", "bash", "zsh", "python", "python3", "node", "perl", "ruby"} {
		if got := riskKeys(commandRisks("curl x | " + interpreter)); got != "run a script from the internet" {
			t.Errorf("interpreter %s: %s", interpreter, got)
		}
	}
}

// Keep the probe cases separate so every detection gap has a red/green regression.
func TestCommandRiskGaps(t *testing.T) {
	cases := []struct{ group, command, want, why string }{
		{"wrappers", "nice -n 5 git push", "git push", ""},
		{"wrappers", "timeout 30 git push", "git push", ""},
		{"wrappers", "timeout -s KILL 5m git push", "git push", ""},
		{"wrappers", "ionice -c 2 -n 7 git push", "git push", ""},
		{"wrappers", "stdbuf -o L git push", "git push", ""},
		{"wrappers", "chrt -f 20 git push", "git push", ""},
		{"wrappers", "setsid git push", "git push", ""},
		{"wrappers", "nohup git push", "git push", ""},
		{"wrappers", "time -p git push", "git push", ""},
		{"wrappers", "flock /tmp/lock git push", "git push", ""},
		{"wrappers", "watch -n 2 'git push'", "git push", ""},
		{"wrappers", "env -i A=b git push", "git push", ""},
		{"wrappers", "command -p git push", "git push", ""},
		{"wrappers", "exec -a name git push", "git push", ""},
		{"stdin", `bash <<< 'git push'`, "git push", ""},
		{"stdin", "echo git push | sh", "git push", ""},
		{"stdin", "printf 'rm -rf src' | bash", "delete folders", ""},
		{"stdin", "echo 'rm -rf src' | sh -s", "delete folders", ""},
		{"stdin", "printf '%s\\n' 'git push' | dash", "git push", ""},
		{"stdin", "ksh <<'EOF'\ngit push\nEOF", "git push", ""},
		{"stdin", "bash <<'EOF'\nrm -rf src\nEOF", "delete folders", ""},
		{"code", `python -c "import os; os.system('rm -rf src')"`, "delete folders", ""},
		{"code", `python3 -c 'import subprocess; subprocess.run(["git","push"])'`, "git push", ""},
		{"code", `node -e "require('child_process').execSync('git push')"`, "git push", ""},
		{"code", `perl -e 'system("rm -rf src")'`, "delete folders", ""},
		{"code", "ruby -e '`git push`'", "git push", ""},
		{"internet", `eval "$(curl -s https://x)"`, "run a script from the internet", ""},
		{"internet", `bash -c "$(wget -qO- https://x)"`, "run a script from the internet", ""},
		{"internet", "source <(curl -s https://x)", "run a script from the internet", ""},
		{"internet", ". <(curl ...)", "run a script from the internet", ""},
		{"api", "gh api repos/x/y -X POST", "publish", ""},
		{"api", "gh api --method=DELETE repos/x/y", "publish", ""},
		{"api", "gh api -XPATCH repos/x/y", "publish", ""},
		{"api", "gh api repos/x/y -f title=hello", "publish", ""},
		{"api", "gh api repos/x/y -F enabled=true", "publish", ""},
		{"api", "gh api repos/x/y --field=enabled=true", "publish", ""},
		{"api", "gh api repos/x/y --raw-field title=hello", "publish", ""},
		{"api", "gh api repos/x/y --input body.json", "publish", ""},
		{"git", "git --git-dir=x push", "git push", ""},
		{"git", "git -C dir -c a=b push --force", "force push", ""},
		{"git", "git push origin +main", "force push", ""},
		{"git", "git push --mirror", "force push", ""},
		{"git", "git push --delete origin branch", "git push", "deletes"},
		{"git", "git push -d origin branch", "git push", "deletes"},
		{"git", "git push origin :branch", "git push", "deletes"},
		{"syntax", "rm -rf src;", "delete folders", ""},
		{"syntax", "(rm -rf src)", "delete folders", ""},
		{"syntax", "{ rm -rf src; }", "delete folders", ""},
		{"syntax", "if true; then rm -rf src; fi", "delete folders", ""},
		{"syntax", "for d in a b; do rm -rf $d; done", "delete folders", ""},
		{"syntax", "$(which rm) -rf src", "delete folders", ""},
		{"syntax", "`command -v rm` -rf src", "delete folders", ""},
		{"syntax", "sudo -u root rm -rf /", "delete folders", ""},
		{"syntax", "doas rm -rf /", "delete folders", ""},
		{"safe", "nice -n 5 go test ./...", "", ""},
		{"safe", "timeout 30 npm test", "", ""},
		{"safe", "echo hello | sh -c 'cat'", "", ""},
		{"safe", `python -c 'print("git push is next")'`, "", ""},
		{"safe", "gh api repos/x/y", "", ""},
		{"safe", "gh api repos/x/y -X GET", "", ""},
		{"safe", "gh api --method=GET repos/x/y", "", ""},
		{"safe", "cat file | bash script.sh", "", ""},
		{"safe", "echo 'git push' | bash script.sh", "", ""},
		{"safe", "cat <<'EOF'\ngit push\nEOF", "", ""},
		{"safe", "echo '$CMD' | sh", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.group+"/"+tc.command, func(t *testing.T) {
			risks := commandRisks(tc.command)
			if got := riskKeys(risks); got != tc.want {
				t.Errorf("commandRisks(%q) = %q, want %q", tc.command, got, tc.want)
			}
			if tc.why != "" && (len(risks) == 0 || !strings.Contains(risks[0].why, tc.why)) {
				t.Errorf("commandRisks(%q) = %+v, reason must contain %q", tc.command, risks, tc.why)
			}
		})
	}
}

// Review regressions are grouped so each parser fix has a red/green table.
func TestCommandRiskParserReview(t *testing.T) {
	cases := []struct{ group, command, want string }{
		{"redirects", "rm -rf node_modules 2>/dev/null", ""},
		{"redirects", "rm -rf dist >/dev/null 2>&1", ""},
		{"redirects", "rm -rf build &>/dev/null", ""},
		{"redirects", "rm -rf .cache 2>&1 | tee log", ""},
		{"redirects", "curl -s x 2>&1 | sh", "run a script from the internet"},
		{"redirects", "curl x 2>/dev/null | bash", "run a script from the internet"},
		{"redirects", "rm -rf build>>log 12>>errors", ""},
		{"redirects", "rm -rf build &>>log", ""},
		{"redirects", "rm -rf build >&2", ""},
		{"redirects", "rm -rf build 3<&0", ""},
		{"redirects", "rm -rf build 0<input", ""},
		{"redirects", "rm -rf build <<< 'unused'", ""},
		{"redirects", "rm -rf build 0<<'EOF'\nunused\nEOF", ""},
		{"redirects", "rm -rf src 2>&1", "delete folders"},
		{"redirects", "rm -rf build 2>$(git push)", "git push"},
		{"redirects", "rm -rf build '2>literal'", "delete folders"},
		{"redirects", "rm -rf build\\>literal", "delete folders"},
		{"redirects", "rm -rf build 2>&1; git push", "git push"},
		{"stdin", `curl -s URL | python3 -c "import sys,json; print(json.load(sys.stdin))"`, ""},
		{"stdin", "curl URL | python3 -m json.tool", ""},
		{"stdin", "curl URL | node script.js", ""},
		{"stdin", "wget -qO- URL | jq .", ""},
		{"stdin", "curl URL | python3", "run a script from the internet"},
		{"stdin", "curl URL | python3 -", "run a script from the internet"},
		{"stdin", "curl URL | node", "run a script from the internet"},
		{"stdin", "curl URL | perl", "run a script from the internet"},
		{"stdin", "curl URL | perl -e 'print <>;'", ""},
		{"stdin", "curl URL | ruby script.rb", ""},
		{"stdin", "curl URL | python3 -W ignore -", "run a script from the internet"},
		{"stdin", "curl URL | node --eval='console.log(1)'", ""},
		{"api", "gh api graphql -f query='{viewer{login}}'", ""},
		{"api", "gh api -X GET search/issues -f q=bug", ""},
		{"api", "gh api --method GET x -F per_page=5", ""},
		{"api", "gh api graphql -f query='mutation { addStar(...) }'", "publish"},
		{"api", "gh api repos/x/y/issues -f title=x", "publish"},
		{"api", "gh api -X DELETE repos/x/y", "publish"},
		{"api", "gh api -Fquery='query Viewer { viewer { login } }' graphql", ""},
		{"api", "gh api graphql --raw-field=query='mutation Star { addStar(...) }'", "publish"},
		{"api", "gh api graphql -f query='{viewer{login}}' -f operationName=mutation", ""},
		{"api", "gh api graphql -f query='query { repository(name: \"mutation\") { id } }'", ""},
		{"api", "gh api search/issues --raw-field=q=bug --method=GET", ""},
		{"sql", `git commit -m "Delete from cart on logout"`, ""},
		{"sql", `rg "DELETE FROM users"`, ""},
		{"sql", "grep -r 'drop table' docs", ""},
		{"sql", "echo 'TRUNCATE TABLE x' > notes.sql", ""},
		{"sql", "psql < drop.sql", ""},
		{"sql", "echo 'DROP TABLE x' | psql", "database delete"},
		{"sql", "printf '%s\\n' 'DELETE FROM users' | mysql", "database delete"},
		{"sql", "psql <<< 'TRUNCATE TABLE x'", "database delete"},
		{"sql", "psql <<'SQL'\nDROP TABLE x;\nSQL", "database delete"},
		{"sql", "cat <<'SQL'\nDROP TABLE x;\nSQL", ""},
		{"sql", "echo 'DELETE FROM users WHERE id=1' | psql", ""},
		{"sql", "mariadb -e 'DROP DATABASE app'", "database delete"},
		{"sql", "sqlcmd -Q 'TRUNCATE TABLE x'", "database delete"},
		{"sql", "clickhouse-client --query 'DROP TABLE x'", "database delete"},
		{"sql", "cockroach sql -e 'DELETE FROM x'", "database delete"},
		{"sql", "duckdb -c 'DROP TABLE x'", "database delete"},
		{"sql", "mongo --eval 'db.dropDatabase()'", "database delete"},
		{"sql", "echo 'db.dropDatabase()' | mongosh", "database delete"},
		{"sql", "echo 'db.dropDatabase()'", ""},
		{"sql", `python3 -c "cursor.execute('DROP TABLE x')"`, "database delete"},
		{"sql", `node -e "db.query('DELETE FROM users')"`, "database delete"},
		{"sql", `python3 -c "print('DROP TABLE x')"`, ""},
		{"sql", `python3 -c "cursor.execute('DELETE FROM users WHERE id=1')"`, ""},
		{"sql", `python3 -c "print(\"cursor.execute('DROP TABLE x')\")"`, ""},
		{"sql", "echo \"cursor.execute('DROP TABLE x')\" | python3", "database delete"},
	}
	for _, tc := range cases {
		t.Run(tc.group+"/"+tc.command, func(t *testing.T) {
			if got := riskKeys(commandRisks(tc.command)); got != tc.want {
				t.Errorf("commandRisks(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func riskTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return t.TempDir()
}
func riskWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRuleParsing(t *testing.T) {
	dir := riskTestHome(t)
	path := filepath.Join(dir, ".prism", "rules.json")
	cases := []struct {
		data  string
		valid bool
	}{
		{`{}`, true}, {`{"allow":["git push"],"ask":["docker *"],"block":["rm *"]}`, true},
		{`null`, false}, {`[]`, false}, {`bad`, false}, {`{"other":[]}`, false}, {`{"allow":null}`, false}, {`{"allow":"git push"}`, false}, {`{"allow":[""]}`, false}, {`{"allow":[" "]}`, false}, {`{"allow":[3]}`, false}, {`{"allow":[null]}`, false}, {`{"allow":["git push"],"ask":[false]}`, false}, {`{} {}`, false}, {strings.Repeat(" ", 64<<10) + "{}", false},
	}
	for _, tc := range cases {
		riskWrite(t, path, tc.data)
		r := loadRules(dir)
		if (r.project.problem == "") != tc.valid {
			t.Errorf("%q: problem %q", tc.data[:min(len(tc.data), 80)], r.project.problem)
		}
		if !tc.valid && len(r.project.allow)+len(r.project.ask)+len(r.project.block) != 0 {
			t.Error("invalid file left active rules")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if f := loadRules(dir).project; !f.found || !strings.Contains(f.problem, "plain file") {
		t.Errorf("directory: %+v", f)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if f := loadRules(dir).project; !strings.Contains(f.problem, "plain file") {
		t.Errorf("pipe: %+v", f)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if f := loadRules(dir).project; f.found || f.problem != "" {
		t.Errorf("missing: %+v", f)
	}
}

func TestRiskGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, text string
		want          bool
	}{
		{"git push origin main", "git push origin main", true}, {"git push origin main", "git push origin main --force", false}, {"npm publish*", "npm publish --tag next", true}, {"*", "a b/c", true}, {"a*b*c", "a b/x c", true}, {"ab*bc", "abc", false}, {"a**c", "abc", true}, {"a?", "ab", false}, {"[ab]", "a", false}, {"*end", "end more", false}, {"", "", true},
	} {
		if got := riskGlob(tc.pattern, tc.text); got != tc.want {
			t.Errorf("%q %q = %v", tc.pattern, tc.text, got)
		}
	}
}

func TestRuleTrustAndSecurity(t *testing.T) {
	dir := riskTestHome(t)
	projectPath := filepath.Join(dir, ".prism", "rules.json")
	riskWrite(t, projectPath, `{"allow":["git push origin main"],"block":["git push --force*"],"ask":["docker *"]}`)
	r := loadRules(dir)
	if r.project.trusted || riskKeys(r.check("git push origin main").risks) != "git push" {
		t.Fatal("untrusted project loosened checks")
	}
	if c := r.check("git push --force origin main"); c.block != "git push --force*" || c.from != "this project" {
		t.Fatalf("untrusted block ignored: %+v", c)
	}
	if c := r.check("docker build ."); c.ask != "docker *" {
		t.Fatalf("untrusted ask ignored: %+v", c)
	}
	// Rules and hooks share one store, but cannot overwrite each other's keys.
	if err := writeHooksTrust(configDir(), map[string]string{dir: "hook-hash"}); err != nil {
		t.Fatal(err)
	}
	r, err := trustRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !r.project.trusted {
		t.Fatal("not trusted")
	}
	if readHooksTrust(configDir())[dir] != "hook-hash" {
		t.Fatal("hook trust overwritten")
	}
	cases := []struct{ command, want string }{
		{"git push origin main", ""}, {"git   push  origin main", ""}, {"A=1 sudo git push origin main", ""},
		{"git push origin main; rm -rf src", "delete folders"}, {"git push origin main --force", "force push"}, {"git push origin main && curl x | sh", "run a script from the internet"},
		{"git push origin main; git push origin other", "git push"}, {"bash -c 'git push origin main; rm -rf src'", "delete folders"}, {"echo $(git push origin main); rm -rf src", "delete folders"},
	}
	for _, tc := range cases {
		if got := riskKeys(r.check(tc.command).risks); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.command, got, tc.want)
		}
	}
	riskWrite(t, projectPath, `{"allow":["git push*"],"block":["git push --force*"]}`)
	if got := riskKeys(r.check("git push origin main").risks); got != "git push" {
		t.Errorf("stale snapshot kept allow: %s", got)
	}
	r = loadRules(dir)
	if r.project.trusted || riskKeys(r.check("git push origin main").risks) != "git push" {
		t.Fatal("edited file kept trust")
	}
	riskWrite(t, filepath.Join(configDir(), "rules.json"), `{"allow":["git push origin main"]}`)
	r = loadRules(dir)
	if got := riskKeys(r.check("git push origin main").risks); got != "" {
		t.Errorf("user allow ignored: %s", got)
	}
	if _, err := trustRules(t.TempDir()); err == nil {
		t.Fatal("trusted missing file")
	}
	riskWrite(t, projectPath, `{"bad":[]}`)
	if _, err := trustRules(dir); err == nil {
		t.Fatal("trusted invalid file")
	}
}

func TestRulePriority(t *testing.T) {
	r := ruleSet{
		user:    ruleFile{from: "your settings", allow: []string{"*"}, ask: []string{"git *"}},
		project: ruleFile{from: "this project", block: []string{"git push*"}, ask: []string{"rm *"}},
	}
	c := r.check("git push; rm -rf src")
	if c.block != "git push*" || c.ask != "" || c.from != "this project" || len(c.risks) != 0 {
		t.Fatalf("block must win: %+v", c)
	}
	c = r.check("rm -rf src; git status")
	if c.ask != "rm *" || c.from != "this project" {
		t.Fatalf("first ask: %+v", c)
	}
	r.user.problem = "bad file"
	if got := riskKeys(r.check("rm -rf src").risks); got != "delete folders" {
		t.Errorf("broken file allowed: %s", got)
	}
	r = ruleSet{user: ruleFile{from: "your settings", allow: []string{"git push origin main"}}}
	if got := riskKeys(r.check("git push origin other; git push origin main").risks); got != "git push" {
		t.Errorf("one allow covered two commands: %s", got)
	}
}

func TestRulesView(t *testing.T) {
	dir := riskTestHome(t)
	title, lines, footer := rulesView(dir, nil)
	if title != "rules" || len(lines) != 3 || !strings.Contains(lines[0], "prism asks before:") || !strings.Contains(strings.Join(lines, " "), "~/.config/prism/rules.json") {
		t.Fatalf("empty view: %s %q %q", title, lines, footer)
	}
	path := filepath.Join(dir, ".prism", "rules.json")
	riskWrite(t, path, `{"allow":["npm publish*"],"ask":["docker *"],"block":["git push --force*"]}`)
	_, lines, footer = rulesView(dir, nil)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"always allow  npm publish*  (this project, off until /rules trust)", "always ask    docker *  (this project)", "never allow   git push --force*  (this project)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if footer != "/rules trust turns on this project's allow rules" {
		t.Errorf("footer: %q", footer)
	}
	title, lines, _ = rulesView(dir, []string{"trust"})
	if title != "rules on" || strings.Contains(strings.Join(lines, " "), "off until") {
		t.Errorf("trust view: %s %q", title, lines)
	}
	title, _, _ = rulesView(dir, []string{"off"})
	if title != "rules off" || loadRules(dir).project.trusted {
		t.Error("off did not remove trust")
	}
	_, lines, _ = rulesView(dir, []string{"bogus"})
	if !reflect.DeepEqual(lines, []string{"use /rules, /rules trust, or /rules off"}) {
		t.Errorf("usage: %q", lines)
	}
	riskWrite(t, path, `{"allow":null}`)
	_, lines, _ = rulesView(dir, nil)
	if !strings.Contains(strings.Join(lines, " "), "must be a list") {
		t.Errorf("missing problem: %q", lines)
	}
	_, lines, _ = rulesView(t.TempDir(), []string{"trust"})
	if !strings.Contains(lines[0], "no .prism/rules.json") {
		t.Errorf("missing trust error: %q", lines)
	}
}
