package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type loginRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SignedIn  bool   `json:"signedIn"`
	Finishing bool   `json:"finishing"`
	Pending   bool   `json:"pending"`
	Models    int    `json:"models"`
	Error     string `json:"error"`
}

type loginsMsg struct {
	rows []loginRow
	err  error
}

type loginCodeMsg struct {
	id       string
	code     string
	url      string
	expires  int
	interval int
	browser  bool
	err      error
}

type loginDoneMsg struct {
	id        string
	models    int
	note      string
	err       error
	pasted    bool
	retryable bool // the router still has a pending sign-in after a paste error
}

func fetchLogins(api string) ([]loginRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(api, "/v1")+"/logins", nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		return nil, errors.New(routerRefused)
	}
	var out struct {
		Subscriptions []loginRow `json:"subscriptions"`
		Error         *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, errors.New(out.Error.Message)
	}
	return out.Subscriptions, nil
}

func loginRound(api string, body map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(api, "/v1")+"/logins", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	authorizeRouter(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		return errors.New(routerRefused)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func submitLoginCode(api, id, code string) (models int, note string, err error) {
	var out struct {
		OK     bool     `json:"ok"`
		Models []string `json:"models"`
		Note   string   `json:"note"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := loginRound(api, map[string]any{"id": id, "code": code}, &out); err != nil {
		return 0, "", err
	}
	if out.Error != nil {
		return 0, "", errors.New(out.Error.Message)
	}
	if !out.OK {
		return 0, "", errors.New("that code did not work; copy it again")
	}
	return len(out.Models), out.Note, nil
}

func pasteLoginCode(api, id, code string) tea.Cmd {
	return func() tea.Msg {
		models, note, err := submitLoginCode(api, id, code)
		retryable := false
		if err != nil {
			if rows, fetchErr := fetchLogins(api); fetchErr == nil {
				for _, row := range rows {
					if row.ID == id {
						retryable = row.Pending || row.Finishing
						break
					}
				}
			}
		}
		return loginDoneMsg{id: id, models: models, note: note, err: err, pasted: true, retryable: retryable}
	}
}

const loginPastePrompt = "paste the code or link from the sign-in page here · enter to finish · esc to cancel"

// loginInputHint replaces the chat input placeholder while a browser sign-in
// waits for its pasted code.
const loginInputHint = "paste the sign-in code here"

// loginLines always shows the link, since prism can't know the browser
// opened, plus the device code or the paste instruction.
func loginLines(msg loginCodeMsg) []string {
	if msg.browser {
		return []string{"1. open this link and sign in (prism tries your browser and copies the link to your clipboard):", msg.url,
			"2. " + loginPastePrompt}
	}
	return []string{"1. open this page (prism also tries to open your browser):", msg.url,
		"2. type this code on that page, then allow access:", msg.code,
		"nothing to paste here: prism signs you in by itself once you allow it"}
}

// openBrowser opens link without waiting. Best effort: the link stays on
// screen anyway.
var openBrowser = func(link string) error {
	if os.Getenv("PRISM_NO_BROWSER") != "" {
		return errors.New("browser disabled")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// loginStarted is the side work after a sign-in starts: try the browser and
// put the device code (or the link) on the clipboard through OSC 52.
func loginStarted(msg loginCodeMsg) tea.Cmd {
	clip := msg.url
	if !msg.browser {
		clip = msg.code
	}
	link := msg.url
	return tea.Batch(tea.SetClipboard(clip), func() tea.Msg {
		_ = openBrowser(link)
		return nil
	})
}

// pollLogin runs in a tea.Cmd goroutine until the sign-in finishes: the token
// poll for device-code accounts, the signed-in flag for browser accounts.
func pollLogin(api, id string, interval, expires int, browser bool) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(time.Duration(expires) * time.Second)
		if interval < 3 {
			interval = 3
		}
		for time.Now().Before(deadline) {
			time.Sleep(time.Duration(interval) * time.Second)
			if browser {
				rows, err := fetchLogins(api)
				if err != nil {
					return loginDoneMsg{id: id, err: err}
				}
				for _, row := range rows {
					if row.ID != id {
						continue
					}
					if row.Finishing || row.Pending {
						continue
					}
					if row.Error != "" && (row.Models == 0 || row.Error == "the sign-in timed out; run /login again") {
						return loginDoneMsg{id: id, err: errors.New(row.Error)}
					}
					if row.SignedIn {
						return loginDoneMsg{id: id, models: row.Models, note: row.Error}
					}
					if row.Error != "" {
						return loginDoneMsg{id: id, err: errors.New(row.Error)}
					}
					return loginDoneMsg{id: id, err: errors.New("the sign-in was cancelled")}
				}
				continue
			}
			var out struct {
				Status   string   `json:"status"`
				Interval int      `json:"interval"`
				Models   []string `json:"models"`
				Note     string   `json:"note"`
				Message  string   `json:"message"`
			}
			if err := loginRound(api, map[string]any{"id": id, "poll": true}, &out); err != nil {
				return loginDoneMsg{id: id, err: err}
			}
			switch out.Status {
			case "ok":
				return loginDoneMsg{id: id, models: len(out.Models), note: out.Note}
			case "error":
				return loginDoneMsg{id: id, err: errors.New(out.Message)}
			case "idle":
				return loginDoneMsg{id: id, err: errors.New("the sign-in was not started; run /login " + id + " again")}
			}
			if out.Interval > 0 {
				interval = out.Interval
			}
		}
		return loginDoneMsg{id: id, err: errors.New("the sign-in timed out; run /login " + id + " again")}
	}
}

type loginOutMsg struct {
	id  string
	err error
}

func (m *model) loginCommand(fields []string) tea.Cmd {
	api := m.agent.api
	if len(fields) == 1 {
		m.openMenu(menuState{
			kind:       "login",
			title:      "subscriptions",
			hint:       "↑↓ move · enter sign in · esc close",
			filterable: true,
			noun:       "account",
			empty:      "the router lists no subscription accounts",
			loading:    true,
		})
		return func() tea.Msg {
			rows, err := fetchLogins(api)
			return loginsMsg{rows: rows, err: err}
		}
	}
	id := fields[1]
	if id == "rm" || id == "remove" {
		if len(fields) != 3 {
			m.entries = append(m.entries, &entry{kind: "note", text: "usage: /login rm <name> · /login lists them all"})
			return nil
		}
		return signOut(api, fields[2])
	}
	// /login <id> browser asks for the browser + paste sign-in even when the
	// account defaults to a device code.
	body := map[string]any{"id": id}
	if len(fields) == 3 && fields[2] == "browser" {
		body["browser"] = true
	} else if len(fields) > 2 {
		m.loginFinished = ""

		return pasteLoginCode(api, id, strings.Join(fields[2:], " "))
	}
	return func() tea.Msg {
		// Browser sign-ins answer {url}; device codes {userCode, verificationURI}.
		var out struct {
			UserCode        string `json:"userCode"`
			VerificationURI string `json:"verificationURI"`
			URL             string `json:"url"`
			Expires         int    `json:"expires"`
			Interval        int    `json:"interval"`
			Error           *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := loginRound(api, body, &out); err != nil {
			return loginCodeMsg{id: id, err: err}
		}
		if out.Error != nil {
			return loginCodeMsg{id: id, err: errors.New(out.Error.Message)}
		}
		link := out.VerificationURI
		if out.UserCode == "" && out.URL != "" {
			link = out.URL
		}
		if link == "" {
			link = out.URL
		}
		if link == "" {
			return loginCodeMsg{id: id, err: errors.New("the router sent no sign-in link for " + id + "; try /login " + id + " again")}
		}
		if out.Expires <= 0 {
			out.Expires = 900
			if out.UserCode == "" {
				out.Expires = 600
			}
		}
		return loginCodeMsg{id: id, code: out.UserCode, url: link, expires: out.Expires, interval: out.Interval, browser: out.UserCode == ""}
	}
}

// signOut asks the router to forget an account, off the UI goroutine.
func signOut(api, id string) tea.Cmd {
	return func() tea.Msg {
		out := struct {
			OK    bool `json:"ok"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}{}
		if err := loginRound(api, map[string]any{"id": id, "signOut": true}, &out); err != nil {
			return loginOutMsg{id: id, err: err}
		}
		if out.Error != nil {
			return loginOutMsg{id: id, err: errors.New(out.Error.Message)}
		}
		return loginOutMsg{id: id}
	}
}

// loginsLoaded fills the /login menu. A signed-in account gets a
// "sign out of" row under it.
func (m *model) loginsLoaded(msg loginsMsg) {
	m.fillMenu("login", loginItems(msg.rows), msg.err)
}

func loginItems(rows []loginRow) []menuItem {
	var items []menuItem
	for _, row := range rows {
		id, name := row.ID, row.Name
		if name == "" {
			name = id
		}
		it := menuItem{label: id, detail: name, dot: "◌", state: "not signed in"}
		if row.SignedIn {
			it.dot, it.dotTone, it.stateTone = "●", toneOK, toneOK
			it.state = "signed in · " + countModels(row.Models)
		}
		if row.Finishing || row.Pending {
			it.state = "signing in…"
		}
		if row.Error != "" {
			it.dotTone, it.stateTone = toneErr, toneErr
			it.state += " · " + row.Error
		}
		it.run = func(m *model) tea.Cmd { return m.loginCommand([]string{"/login", id}) }
		items = append(items, it)
		if row.SignedIn {
			items = append(items, menuItem{sub: true, label: "sign out of " + name, match: id + " " + name + " sign out",
				run: func(m *model) tea.Cmd { return signOut(m.agent.api, id) }})
		}
	}
	return items
}

func loginsView(rows []loginRow) cmdView {
	v := cmdView{title: "subscriptions", footer: "/login <name> signs in · /login rm <name> signs out · subscription models answer when picked by name"}
	block := cmdBlock{title: "plans you already pay for"}
	for _, row := range rows {
		it := cmdItem{name: row.ID, desc: "not signed in", dot: "◌"}
		if row.SignedIn {
			it.dot, it.dotOK = "●", true
			it.desc = "signed in · " + countModels(row.Models)
		}
		if row.Finishing || row.Pending {
			it.desc = "signing in…"
		}
		block.items = append(block.items, it)
	}
	if len(block.items) > 0 {
		v.blocks = append(v.blocks, block)
	}
	return v
}

func countModels(n int) string {
	if n == 1 {
		return "1 model"
	}
	return fmt.Sprintf("%d models", n)
}
