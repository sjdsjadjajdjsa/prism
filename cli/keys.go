package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Shared across platforms; routerauth.go re-execs on Unix to remove the token
// from the initial environment block too. Windows retains Unsetenv behaviour.
var routerToken = os.Getenv("PRISM_ROUTER_TOKEN")

func init() { os.Unsetenv("PRISM_ROUTER_TOKEN") }

func authorizeRouter(req *http.Request) {
	if routerToken != "" {
		req.Header.Set("x-prism-token", routerToken)
	}
}

const routerRefused = "the router refused this (admin token missing); restart prism"

// keyRow is one provider in the router's key list.
type keyRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Var     string `json:"var"`
	Paid    bool   `json:"paid"`
	Keyless bool   `json:"keyless"`
	KeyURL  string `json:"keyUrl"`
	Set     string `json:"set"`
}

// keysInfo is the router's GET /keys reply: built-in free keys plus presets.
type keysInfo struct {
	Free    []keyRow `json:"free"`
	Presets []keyRow `json:"presets"`
}

type keysMsg struct {
	info keysInfo
	err  error
}

type keySetMsg struct {
	id     string
	masked string
	models int
	note   string
	err    error
}

func fetchKeys(api string) (keysInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(api, "/v1")+"/keys", nil)
	if err != nil {
		return keysInfo{}, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return keysInfo{}, errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		return keysInfo{}, errors.New(routerRefused)
	}
	var info keysInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return keysInfo{}, err
	}
	return info, nil
}

// postKey sets (value) or removes (empty value) a key. id is a preset id, a
// preset key var, or a built-in provider name. The probe on the router side can
// take a few seconds, so the timeout is generous.
func postKey(api, id, value string) (keySetMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"id": id, "value": value})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(api, "/v1")+"/keys", bytes.NewReader(body))
	if err != nil {
		return keySetMsg{}, err
	}
	req.Header.Set("content-type", "application/json")
	authorizeRouter(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return keySetMsg{}, errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		return keySetMsg{}, errors.New(routerRefused)
	}
	var out struct {
		OK     bool   `json:"ok"`
		Masked string `json:"masked"`
		Models *int   `json:"models"`
		Note   string `json:"note"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return keySetMsg{}, err
	}
	if out.Error != nil {
		return keySetMsg{}, errors.New(out.Error.Message)
	}
	msg := keySetMsg{masked: out.Masked, note: out.Note}
	if out.Models != nil {
		msg.models = *out.Models
	}
	return msg, nil
}

// keyCommand runs /key: pick a provider, set, or remove a provider key.
func (m *model) keyCommand(fields []string) tea.Cmd {
	api := m.agent.api
	if len(fields) == 1 {
		m.openMenu(menuState{
			kind:       "key",
			title:      "keys",
			hint:       "↑↓ move · enter type the key · esc close",
			filterable: true,
			noun:       "provider",
			empty:      "the router lists no providers",
			loading:    true,
		})
		return func() tea.Msg {
			info, err := fetchKeys(api)
			return keysMsg{info: info, err: err}
		}
	}
	if fields[1] == "rm" || fields[1] == "remove" {
		if len(fields) != 3 {
			m.entries = append(m.entries, &entry{kind: "note", text: "usage: /key rm <name> · /key lists them all"})
			return nil
		}
		id := fields[2]
		return func() tea.Msg {
			msg, err := postKey(api, id, "")
			msg.id = id
			if err != nil {
				msg.err = err
			}
			return msg
		}
	}
	if len(fields) == 2 {
		m.entries = append(m.entries, &entry{kind: "note", text: "usage: /key <name> <secret> · /key rm <name> · /key lists them all"})
		return nil
	}
	if len(fields) > 3 {
		m.entries = append(m.entries, &entry{kind: "note", text: "the key is one word: /key " + fields[1] + " <secret>"})
		return nil
	}
	id, value := fields[1], fields[2]
	return func() tea.Msg {
		msg, err := postKey(api, id, value)
		msg.id = id
		if err != nil {
			msg.err = err
		}
		return msg
	}
}

// keysLoaded fills the /key menu.
func (m *model) keysLoaded(msg keysMsg) {
	m.fillMenu("key", keyMenuItems(msg.info), msg.err)
}

// keyMenuItems lists the built-in free providers and the presets with their
// masked state. Enter puts "/key <id> " in the input box so the secret is
// typed or pasted there, never shown here; a set key also gets a remove row.
func keyMenuItems(info keysInfo) []menuItem {
	var items []menuItem
	add := func(row keyRow, detail string) {
		id := row.ID
		it := menuItem{label: id, detail: detail, dot: "◌", state: "not set"}
		if row.Set != "" {
			it.dot, it.dotTone, it.state, it.stateTone = "●", toneOK, row.Set, toneOK
		}
		it.run = func(m *model) tea.Cmd {
			m.input = []rune("/key " + id + " ")
			return nil
		}
		items = append(items, it)
		if row.Set != "" {
			items = append(items, menuItem{sub: true, label: "remove " + id + " key", match: id + " remove",
				run: func(m *model) tea.Cmd { return m.keyCommand([]string{"/key", "rm", id}) }})
		}
	}
	for _, row := range info.Free {
		add(row, "free")
	}
	for _, row := range info.Presets {
		if row.Keyless {
			continue
		}
		detail := row.Name
		if detail == "" || detail == row.ID {
			detail = "your own"
		}
		add(row, detail)
	}
	return items
}

func keyItem(row keyRow) cmdItem {
	it := cmdItem{name: row.ID, desc: "not set", dot: "◌"}
	if row.Set != "" {
		it.desc = row.Set
		it.dot, it.dotOK = "●", true
	}
	if row.KeyURL != "" {
		it.sub = row.KeyURL
	}
	return it
}

func keysView(info keysInfo) cmdView {
	v := cmdView{title: "keys"}
	free, paid := cmdBlock{title: "free"}, cmdBlock{title: "paid"}
	var names []string
	for _, row := range append(append([]keyRow{}, info.Free...), info.Presets...) {
		if row.Keyless {
			continue
		}
		if row.Set == "" {
			names = append(names, row.ID)
			continue
		}
		it := keyItem(row)
		it.sub = ""
		if row.Paid {
			paid.items = append(paid.items, it)
		} else {
			free.items = append(free.items, it)
		}
	}
	for _, block := range []cmdBlock{free, paid} {
		if len(block.items) > 0 {
			v.blocks = append(v.blocks, block)
		}
	}
	if len(names) > 0 {
		v.footer = "add one: /key <name> <secret> · names: " + strings.Join(names, ", ")
	}
	return v
}

func keyState(set string) string {
	if set == "" {
		return "not set"
	}
	return set
}

func keyLines(info keysInfo) []string {
	var lines []string
	v := keysView(info)
	for _, block := range v.blocks {
		lines = append(lines, block.title+":")
		for _, it := range block.items {
			lines = append(lines, fmt.Sprintf("  %-14s %s", it.name, it.desc))
		}
	}
	if v.footer != "" {
		lines = append(lines, v.footer)
	}
	return lines
}
