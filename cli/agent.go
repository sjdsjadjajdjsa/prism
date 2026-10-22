package main

import (
	"bufio"
	"bytes"
	"context"
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
}

func newAgent(api, dir, project string) *Agent {
	a := &Agent{api: strings.TrimSuffix(api, "/"), dir: dir, window: contextLimit, store: openStore(project)}
	a.quick = os.Getenv("PRISM_THINK") == "off"
	if n, err := strconv.Atoi(os.Getenv("PRISM_CONTEXT")); err == nil && n >= 2000 {
		a.window = n
	}
	a.reset()
	return a
}

func (a *Agent) reset() {
	a.messages = []Message{{Role: "system", Content: a.systemPrompt()}}
	a.tokens, a.memMark = 0, 0
	a.session = time.Now().Format("20060102-150405")
}

func (a *Agent) systemPrompt() string {
	prompt := basePrompt(a.dir)
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
- Never delete or overwrite files the user asked for or already had, unless they asked you to.
- Before you say you are done, confirm the files you made still exist and do what was asked.
- When finished, reply briefly in plain words: what you changed and how you checked it.`, dir)
}

func (a *Agent) Run(ctx context.Context, prompt string, emit func(any)) {
	a.messages = append(a.messages, Message{Role: "user", Content: prompt})
	err := a.loop(ctx, emit)
	a.saveSession()
	emit(evDone{err: err})
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
			result := runTool(ctx, a.dir, call.Function.Name, call.Function.Arguments)
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
	for attempt := 0; ; attempt++ {
		reply, err := a.complete(ctx, a.messages, nil, emit)
		if err == nil || ctx.Err() != nil || errors.As(err, &re) || attempt == 2 {
			return reply, err
		}
		emit(evNote{"reply dropped, asking again…"})
	}
}

func (a *Agent) compact(ctx context.Context, emit func(any)) error {
	emit(evNote{"compacting the conversation so it stays sharp…"})
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
		a.messages[0],
		{Role: "user", Content: summaryPrefix + " (earlier messages were compacted):\n\n" + summary + "\n\nThe user's latest request, word for word:\n" + a.latestRequest() + "\n\nContinue from where you left off."},
	}
	a.memMark = len(a.messages)
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
		"tools":          toolSpecs,
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
	if route := res.Header.Get("x-free-route"); route != "" {
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
