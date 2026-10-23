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
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return keySetMsg{}, errors.New("can't reach the free AI router · run: prism status")
	}
	defer res.Body.Close()
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

// keyCommand runs /key: list, set, or remove a provider key.
func (m *model) keyCommand(fields []string) tea.Cmd {
	api := m.agent.api
	if len(fields) == 1 {
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
	v := cmdView{title: "keys", footer: "/key <name> <secret> adds one · /key rm <name> removes it · your own models answer only when picked by name"}
	free := cmdBlock{title: "free (built in)"}
	for _, row := range info.Free {
		free.items = append(free.items, keyItem(row))
	}
	if len(free.items) > 0 {
		v.blocks = append(v.blocks, free)
	}
	yours := cmdBlock{title: "your own models"}
	for _, row := range info.Presets {
		if row.Keyless {
			continue
		}
		yours.items = append(yours.items, keyItem(row))
	}
	if len(yours.items) > 0 {
		v.blocks = append(v.blocks, yours)
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
	lines = append(lines, "free (built in):")
	for _, row := range info.Free {
		lines = append(lines, fmt.Sprintf("  %-14s %s", row.ID, keyState(row.Set)))
	}
	lines = append(lines, "", "your own models:")
	for _, row := range info.Presets {
		if row.Keyless {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %-14s %s", row.ID, keyState(row.Set)))
	}
	lines = append(lines, "", "/key <name> <secret> adds one · /key rm <name> removes it")
	return lines
}
