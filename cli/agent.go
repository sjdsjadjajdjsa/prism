package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxSteps     = 80
	maxReply     = 8192
	contextLimit = 128000
)

const compactPrompt = `Summarize this conversation for yourself so you can continue the work with no other context. Include the user's goal and latest request, key decisions, every file read or changed and how, commands run and their results, problems found, and exactly what remains to do next. Be concise but complete. Do not call tools.`

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type evRoute struct {
	route string
	wait  time.Duration
}

var errCutOff = errors.New("the reply was cut off before it finished")

type evThinking struct{}
type evReasoning struct{ text string }
type evText struct{ text string }
type evToolStart struct{ id, name, arg string }
type evDraft struct {
	name, arg string
	chars     int
}
type evToolDone struct {
	id     string
	result toolResult
}
type evUsage struct{ tokens, window int }
type evNote struct{ text string }
type evDone struct{ err error }

type routeFailure struct {
	Route  string `json:"route"`
	Status int    `json:"status"`
}

type routerError struct {
	message  string
	failures []routeFailure
}

func (e *routerError) Error() string { return e.message }

type Agent struct {
	api        string
	dir        string
	window     int
	tokens     int
	lastPrompt int
	sent       int
	requests   int
	messages   []Message
	store      *store
	session    string
	memMark    int
	quick      bool
	light      bool
	lastRoute  string
	skills     []skill
	mcp        *mcpHub
	system     bool
	approve    func(ctx context.Context, tool, arg string) bool
}

type evApprove struct {
	tool, arg string
	reply     chan bool
}

var needsApproval = map[string]bool{"bash": true, "write": true, "edit": true}

func newAgent(api, dir, project string) *Agent {
	a := &Agent{api: strings.TrimSuffix(api, "/"), dir: dir, window: contextLimit, store: openStore(project)}
	a.quick = os.Getenv("PRISM_THINK") == "off"
	if n, err := strconv.Atoi(os.Getenv("PRISM_CONTEXT")); err == nil && n >= 2000 {
		a.window = n
	}
	a.reset()
	return a
}

func (a *Agent) tools() []any {
	out := append([]any{}, toolSpecs...)
	if len(a.skills) > 0 {
		out = append(out, skillSpec)
	}
	return append(out, a.mcp.specs()...)
}

func (a *Agent) reset() {
	a.messages = []Message{{Role: "system", Content: a.systemPrompt()}}
	a.tokens, a.memMark = 0, 0
	a.session = time.Now().Format("20060102-150405") + "-" + randomSuffix()
}

// randomSuffix returns 4 hex characters so that sessions started within the
// same second get distinct ids.
func randomSuffix() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
	}
	return fmt.Sprintf("%x", b[:])
}

func (a *Agent) systemPrompt() string {
	prompt := basePrompt(a.dir)
	if a.system {
		prompt += systemHelpPrompt
	}
	prompt += skillIndex(a.skills)
	if name, rules := projectRules(a.dir); rules != "" {
		prompt += "\n\nProject rules from " + name + " (always follow these):\n" + rules
	}
	if a.store != nil {
		if mem := a.store.memory(); mem != "" {
			if len(mem) > memoryLimit {
				mem = mem[:memoryLimit]
			}
			prompt += "\n\nWhat you remember about this project from earlier sessions (may be out of date; check before relying on it):\n" + mem
		}
	}
	return prompt
}

func basePrompt(dir string) string {
	return fmt.Sprintf(`You are Prism, a coding agent. You work in the project folder %s on Linux, where bash, git, python3, node and ripgrep are available.

Work like a careful senior engineer:
- If a question can be answered without the project files (general knowledge, math, explanations, greetings), answer directly without tools.
- Look before you change anything: use list, grep and read to understand the code. Never guess what a file contains.
- Make focused changes with edit. Use write only for new files or full rewrites.
- After changing code, run it or its tests with bash to prove it works, then fix whatever fails.
- Keep using tools until the task is done. Ask the user only when something is truly unclear.
- Never start servers or other commands that keep running; check work with commands that finish.
- Never delete or overwrite files that existed before you started or that the user asked for, unless they asked you to. Files you create for the task are the result: never remove them to "clean up".
- Before you say you are done, confirm the files you created still exist and contain the expected changes.
- When finished, reply briefly in plain words: what you changed and how you checked it.`, dir)
}

func (a *Agent) Run(ctx context.Context, prompt string, emit func(any)) {
	a.messages = append(a.messages, Message{Role: "user", Content: prompt})
	err := a.loop(ctx, emit)
	if errors.Is(err, context.Canceled) {
		a.stopped()
	}
	a.saveSession()
	emit(evDone{err: err})
}

// stopped closes the history after the user cancelled a run so the next
// message is not read as a request to resume the old task: every pending
// tool call gets its tool message and a final assistant note is added unless
// the history already ends with a plain assistant reply.
func (a *Agent) stopped() {
	for i := len(a.messages) - 1; i >= 0; i-- {
		m := a.messages[i]
		if m.Role != "assistant" {
			continue
		}
		if len(m.ToolCalls) == 0 {
			if i == len(a.messages)-1 {
				return
			}
			break
		}
		answered := map[string]bool{}
		for _, t := range a.messages[i+1:] {
			if t.Role == "tool" {
				answered[t.ToolCallID] = true
			}
		}
		for _, call := range m.ToolCalls {
			if !answered[call.ID] {
				a.messages = append(a.messages, Message{Role: "tool", ToolCallID: call.ID, Content: "Cancelled by the user."})
			}
		}
		break
	}
	a.messages = append(a.messages, Message{Role: "assistant", Content: "Stopped by the user before finishing. Wait for the next instruction."})
}

func (a *Agent) loop(ctx context.Context, emit func(any)) error {
	for step := range maxSteps {
		a.light = step == 0 && len([]rune(a.latestRequest())) <= 80
		a.prune()
		if a.tokens > a.window*7/10 {
			a.tokens = estimate(a.messages)
		}
		if a.tokens > a.window*7/10 {
			if err := a.compact(ctx, emit); err != nil {
				return err
			}
		}
		reply, err := a.completeRetry(ctx, emit)
		if err != nil {
			return err
		}
		a.messages = append(a.messages, reply)
		emit(evUsage{a.tokens, a.window})
		if len(reply.ToolCalls) == 0 {
			return nil
		}
		for _, call := range reply.ToolCalls {
			if ctx.Err() != nil {
				a.messages = append(a.messages, Message{Role: "tool", ToolCallID: call.ID, Content: "Cancelled by the user."})
				continue
			}
			emit(evToolStart{call.ID, call.Function.Name, strings.TrimPrefix(describe(call.Function.Arguments), a.dir+"/")})
			var result toolResult
			switch {
			case call.Function.Name == "skill":
				result = a.useSkill(call.Function.Arguments)
			case a.isSkill(call.Function.Name):
				result = a.useSkill(`{"name":"` + call.Function.Name + `"}`)
			case a.approve != nil && (needsApproval[call.Function.Name] || a.mcp.has(call.Function.Name)) && !a.approve(ctx, call.Function.Name, approvalText(call.Function.Name, call.Function.Arguments)):
				result = toolResult{output: "The user said no to this. Do not try it again another way; explain what you wanted to do and ask what they prefer.", summary: "you said no"}
			case a.mcp.has(call.Function.Name):
				result = a.mcp.run(ctx, call.Function.Name, call.Function.Arguments)
			default:
				result = runTool(ctx, a.dir, call.Function.Name, call.Function.Arguments)
			}
			emit(evToolDone{call.ID, result})
			a.messages = append(a.messages, Message{Role: "tool", ToolCallID: call.ID, Content: result.output})
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("stopped after %d steps without finishing", maxSteps)
}

func (a *Agent) completeRetry(ctx context.Context, emit func(any)) (Message, error) {
	var re *routerError
	var avoid []string
	for attempt := 0; ; attempt++ {
		var extra map[string]any
		if len(avoid) > 0 {
			extra = map[string]any{"prism_avoid": avoid}
		}
		reply, err := a.complete(ctx, a.messages, extra, emit)
		if a.lastRoute != "" {
			avoid = append(avoid, a.lastRoute)
		}
		if err == nil || ctx.Err() != nil || errors.As(err, &re) || attempt == 2 {
			return reply, err
		}
		emit(evNote{"reply dropped, asking again…"})
	}
}

func (a *Agent) compact(ctx context.Context, emit func(any)) error {
	emit(evNote{"compacting the conversation so it stays sharp…"})
	light := a.light
	a.light = false
	defer func() { a.light = light }()
	request := append(append([]Message{}, a.messages...), Message{Role: "user", Content: compactPrompt})
	reply, err := a.complete(ctx, request, map[string]any{"tool_choice": "none"}, func(any) {})
	if err != nil {
		return fmt.Errorf("compacting failed: %w", err)
	}
	summary := strings.TrimSpace(reply.Content)
	if summary == "" {
		return errors.New("compacting failed: the model returned an empty summary")
	}
	a.messages = []Message{
		{Role: "system", Content: a.systemPrompt()},
		{Role: "user", Content: summaryPrefix + " (earlier messages were compacted):\n\n" + summary + "\n\nThe user's latest request, word for word:\n" + a.latestRequest() + "\n\nContinue from where you left off."},
	}
	a.memMark = 1
	a.tokens = estimate(a.messages)
	emit(evNote{"conversation compacted"})
	emit(evUsage{a.tokens, a.window})
	return nil
}

func estimate(messages []Message) int {
	chars := 0
	for _, m := range messages {
		chars += len(m.Content)
		for _, call := range m.ToolCalls {
			chars += len(call.Function.Arguments)
		}
	}
	return chars / 4
}

func (a *Agent) complete(ctx context.Context, messages []Message, extra map[string]any, emit func(any)) (Message, error) {
	body := map[string]any{
		"model":          "auto",
		"messages":       messages,
		"tools":          a.tools(),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"max_tokens":     maxReply,
	}
	if a.quick || a.light {
		body["prism_quick"] = true
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.api+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("content-type", "application/json")
	a.lastRoute = ""
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Message{}, ctx.Err()
		}
		return Message{}, fmt.Errorf("can't reach the free AI router at %s. Run: prism status", a.api)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(res.Body)
		var failure struct {
			Error struct {
				Message  string         `json:"message"`
				Failures []routeFailure `json:"failures"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			return Message{}, &routerError{message: failure.Error.Message, failures: failure.Error.Failures}
		}
		return Message{}, fmt.Errorf("router error %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	a.lastRoute = res.Header.Get("x-free-route")
	if route := a.lastRoute; route != "" {
		wait, _ := strconv.Atoi(res.Header.Get("x-free-wait"))
		emit(evRoute{route, time.Duration(wait) * time.Millisecond})
	}
	before := a.tokens
	var reply Message
	if strings.Contains(res.Header.Get("content-type"), "event-stream") {
		reply, err = a.readStream(res.Body, emit)
	} else {
		reply, err = a.readWhole(res.Body, emit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return Message{}, ctx.Err()
		}
		return Message{}, err
	}
	if a.tokens == before {
		a.tokens = estimate(append(messages, reply))
	}
	prompt := a.lastPrompt
	if prompt == 0 {
		prompt = estimate(messages)
	}
	a.lastPrompt = 0
	a.sent += prompt
	a.requests++
	addUsage(prompt)
	for i := range reply.ToolCalls {
		if reply.ToolCalls[i].ID == "" {
			reply.ToolCalls[i].ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
		}
		reply.ToolCalls[i].Type = "function"
	}
	return reply, nil
}

type usage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (a *Agent) record(u *usage) {
	if u == nil {
		return
	}
	if u.PromptTokens > 0 {
		a.lastPrompt = u.PromptTokens
	}
	if u.TotalTokens > 0 {
		a.tokens = u.TotalTokens
	} else if u.PromptTokens > 0 {
		a.tokens = u.PromptTokens
	}
}

type streamChunk struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int          `json:"index"`
				ID       string       `json:"id"`
				Function FunctionCall `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Agent) readStream(body io.Reader, emit func(any)) (Message, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var text strings.Builder
	calls := map[int]*ToolCall{}
	slots := map[int]int{}
	var order []int
	thinking, ended := false, false
	lastDraft := 0
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			ended = true
			break
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return Message{}, errors.New(chunk.Error.Message)
		}
		a.record(chunk.Usage)
		for _, choice := range chunk.Choices {
			delta := choice.Delta
			if choice.FinishReason != "" {
				ended = true
			}
			if reasoning := delta.ReasoningContent + delta.Reasoning; reasoning != "" {
				if !thinking {
					thinking = true
					emit(evThinking{})
				}
				emit(evReasoning{reasoning})
			}
			if delta.Content != "" {
				text.WriteString(delta.Content)
				if text.Len() > 0 && strings.TrimSpace(text.String()) != "" {
					emit(evText{delta.Content})
				}
			}
			for _, part := range delta.ToolCalls {
				key, seen := slots[part.Index]
				if seen && part.ID != "" && calls[key].ID != "" && calls[key].ID != part.ID {
					seen = false
				}
				if !seen {
					key = len(order)
					slots[part.Index] = key
					calls[key] = &ToolCall{}
					order = append(order, key)
				}
				call := calls[key]
				if part.ID != "" {
					call.ID = part.ID
				}
				if call.Function.Name == "" {
					call.Function.Name = part.Function.Name
				}
				call.Function.Arguments += part.Function.Arguments
				if n := len(call.Function.Arguments); n-lastDraft >= 200 || lastDraft == 0 {
					lastDraft = n
					emit(evDraft{call.Function.Name, draftArg(call.Function.Arguments), n})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, err
	}
	if !ended {
		return Message{}, errCutOff
	}
	reply := Message{Role: "assistant", Content: text.String()}
	for _, index := range order {
		reply.ToolCalls = append(reply.ToolCalls, *calls[index])
	}
	return reply, nil
}

func (a *Agent) readWhole(body io.Reader, emit func(any)) (Message, error) {
	var whole struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage *usage `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&whole); err != nil {
		return Message{}, fmt.Errorf("unreadable reply from the router: %w", err)
	}
	if len(whole.Choices) == 0 {
		return Message{}, errors.New("the model sent an empty reply")
	}
	a.record(whole.Usage)
	reply := whole.Choices[0].Message
	reply.Role = "assistant"
	if reply.Content != "" {
		emit(evText{reply.Content})
	}
	return reply, nil
}

var draftPath = regexp.MustCompile(`"(?:path|command|pattern)"\s*:\s*"((?:[^"\\]|\\.){1,120})`)

func draftArg(args string) string {
	if m := draftPath.FindStringSubmatch(args); m != nil {
		return m[1]
	}
	return ""
}

const systemHelpPrompt = `

COMPUTER HELP MODE: you are running directly on the user's own computer (not in a sandbox) to help troubleshoot it. The user is probably not technical.
- Start with read-only checks (uname -a, cat /etc/os-release, df -h, free -h, ip a, systemctl --failed, journalctl -p 3 -b --no-pager | tail -40, dmesg | tail).
- Every bash, write and edit call is shown to the user, who must approve it first. Run one clear command at a time and say in plain words what it does and why.
- Never run destructive commands (rm -rf, dd, mkfs, chmod -R on system folders, editing boot files) unless the user explicitly asks and understands the risk.
- Commands that need sudo will ask the user for their password in their own terminal; keep them to a minimum.
- Explain what you found in plain words, then the fix.`

func approvalText(tool, raw string) string {
	args := argsOf(raw)
	switch tool {
	case "bash":
		return sarg(args, "command")
	case "write":
		return "write the file " + sarg(args, "path")
	case "edit":
		return "change the file " + sarg(args, "path")
	}
	if isMCP(tool) {
		server, name, _ := strings.Cut(tool, "__")
		return "use the " + name + " tool from " + server + " with " + clipText(raw, 300)
	}
	return tool
}

func isMCP(name string) bool { return strings.Contains(name, "__") }
