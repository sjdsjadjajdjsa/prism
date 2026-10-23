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
	ID       string `json:"id"`
	Name     string `json:"name"`
	SignedIn bool   `json:"signedIn"`
	Models   int    `json:"models"`
	Error    string `json:"error"`
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
	id     string
	models int
	note   string
	err    error
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
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

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
					if row.SignedIn {
						return loginDoneMsg{id: id, models: row.Models}
					}
					if row.Error != "" {
						return loginDoneMsg{id: id, err: errors.New(row.Error)}
					}
				}
				continue
			}
			var out struct {
				Status   string `json:"status"`
				Interval int    `json:"interval"`
				Models   int    `json:"models"`
				Note     string `json:"note"`
				Message  string `json:"message"`
			}
			if err := loginRound(api, map[string]any{"id": id, "poll": true}, &out); err != nil {
				return loginDoneMsg{id: id, err: err}
			}
			switch out.Status {
			case "ok":
				return loginDoneMsg{id: id, models: out.Models, note: out.Note}
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

// loginCommand runs /login: list accounts, start a device-code sign-in, or sign out.
func (m *model) loginCommand(fields []string) tea.Cmd {
	api := m.agent.api
	if len(fields) == 1 {
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
		out := struct {
			OK    bool `json:"ok"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}{}
		if err := loginRound(api, map[string]any{"id": fields[2], "signOut": true}, &out); err != nil {
			m.entries = append(m.entries, &entry{kind: "note", text: err.Error()})
			return nil
		}
		if out.Error != nil {
			m.entries = append(m.entries, &entry{kind: "note", text: out.Error.Message})
			return nil
		}
		m.entries = append(m.entries, &entry{kind: "note", text: "signed out of " + fields[2]})
		return nil
	}
	if len(fields) != 2 {
		m.entries = append(m.entries, &entry{kind: "note", text: "usage: /login <name> · /login lists them all"})
		return nil
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
		}
		return loginCodeMsg{id: id, code: out.UserCode, url: out.VerificationURI, expires: out.Expires, browser: out.UserCode == ""}
	}
}

func loginsView(rows []loginRow) cmdView {
	v := cmdView{title: "subscriptions", footer: "/login <name> signs in · /login rm <name> signs out · subscription models answer when picked by name"}
	block := cmdBlock{title: "plans you already pay for"}
	for _, row := range rows {
		it := cmdItem{name: row.ID, desc: "not signed in", dot: "◌"}
		if row.SignedIn {
			it.dot, it.dotOK = "●", true
			if row.Models > 0 {
				it.desc = fmt.Sprintf("signed in · %d models", row.Models)
			} else {
				it.desc = "signed in · no models listed yet"
			}
		}
		block.items = append(block.items, it)
	}
	if len(block.items) > 0 {
		v.blocks = append(v.blocks, block)
	}
	return v
}
