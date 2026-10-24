package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// loginRow is one subscription account in the router's /logins list.
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
	id      string
	code    string
	url     string
	expires int
	browser bool
	err     error
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

// loginRound does one call to the router's /logins endpoint.
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

// submitLoginCode finishes a pending browser sign-in using a code or callback URL.
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

// pollLogin loops until the sign-in finishes: the router's token poll for
// device-code accounts, or the signed-in flag for browser accounts. It runs in
// a tea.Cmd goroutine.
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

// loginOutMsg is the router's answer to a sign-out.
type loginOutMsg struct {
	id  string
	err error
}

// loginCommand runs /login: pick an account, start a sign-in, or sign out.
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
	if len(fields) > 2 {
		m.loginFinished = ""

		return pasteLoginCode(api, id, strings.Join(fields[2:], " "))
	}
	return func() tea.Msg {
		var out struct {
			UserCode        string `json:"userCode"`
			VerificationURI string `json:"verificationURI"`
			Expires         int    `json:"expires"`
			Error           *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := loginRound(api, map[string]any{"id": id}, &out); err != nil {
			return loginCodeMsg{id: id, err: err}
		}
		if out.Error != nil {
			return loginCodeMsg{id: id, err: errors.New(out.Error.Message)}
		}
		if out.Expires <= 0 {
			out.Expires = 900
			if out.UserCode == "" {
				out.Expires = 600
			}
		}
		return loginCodeMsg{id: id, code: out.UserCode, url: out.VerificationURI, expires: out.Expires, browser: out.UserCode == ""}
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

// loginsLoaded fills the /login menu: every account with its state and last
// error; enter signs in like /login <id>, and a signed-in account gets a
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

// countModels says "1 model" or "N models".
func countModels(n int) string {
	if n == 1 {
		return "1 model"
	}
	return fmt.Sprintf("%d models", n)
}

func countFree(n int) string {
	if n == 1 {
		return "1 free model ready"
	}
	return fmt.Sprintf("%d free models ready", n)
}
