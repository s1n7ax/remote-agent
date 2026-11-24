package claude

// Wire format of `claude -p --output-format stream-json --verbose`, as
// verified against claude 2.1.308:
//
//   - {"type":"system","subtype":"init","session_id":…,"model":…} first (seen
//     by running the CLI unauthenticated).
//   - {"type":"assistant","message":{"content":[{"type":"text"|"thinking"|
//     "tool_use",…}]},"error":"authentication_failed"|"rate_limit"|…} — the
//     error field is top-level, next to message (seen on a live run).
//   - {"type":"user","message":{"content":[{"type":"tool_result",
//     "tool_use_id","content":string|[blocks],"is_error"}]}}.
//   - {"type":"rate_limit_event","rate_limit_info":{"status":"allowed"|
//     "allowed_warning"|"rejected","resetsAt":<epoch s>,"rateLimitType":
//     "five_hour"|"seven_day"|…,"utilization":<0..1>}} (zod schema in the
//     bundled CLI).
//   - {"type":"result","subtype":…,"is_error":bool,"result":text,
//     "total_cost_usd":n,"api_error_status":null|int} (live run).
//   - {"type":"control_response","response":{"subtype":"success"|"error",
//     "request_id","error"}} answers our control_request (CLI schema).
//
// Input lines accept "priority":"now"|"next"|"later" (CLI schema).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/agent/proc"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// limitText matches the CLI's "You've hit your session limit · resets …".
var limitText = regexp.MustCompile(`(?i)hit your ([a-z0-9-]+ )?limit`)

// defaultLimitWait is assumed when a limit is hit with no known reset time.
const defaultLimitWait = time.Hour

type message struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`

	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Error           string  `json:"error"`
	ParentToolUseID *string `json:"parent_tool_use_id"`

	RateLimitInfo *rateLimitInfo `json:"rate_limit_info"`

	IsError        bool            `json:"is_error"`
	Result         string          `json:"result"`
	TotalCostUSD   float64         `json:"total_cost_usd"`
	APIErrorStatus json.RawMessage `json:"api_error_status"`
	Errors         []string        `json:"errors"`

	Response *struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
	} `json:"response"`
}

type rateLimitInfo struct {
	Status        string   `json:"status"`
	ResetsAt      int64    `json:"resetsAt"`
	RateLimitType string   `json:"rateLimitType"`
	Utilization   *float64 `json:"utilization"`
}

type block struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`

	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// result is the end of a turn.
type result struct {
	summary string
	cost    float64
	err     error
}

// parser turns stdout lines into events. It is not safe for concurrent use.
type parser struct {
	now      func() time.Time
	resetsAt time.Time // last reset time a rate_limit_event reported
	limitTyp string
	limited  bool
}

// handle parses one line. It returns the events to emit and, for a result
// line, the turn's outcome.
func (p *parser) handle(line []byte) ([]agent.Event, *result) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return []agent.Event{logEv(domain.EventError, "unparsed output: "+proc.Truncate(string(line), proc.MaxToolOutput), nil)}, nil
	}
	switch m.Type {
	case "system":
		if m.Subtype == "init" && m.SessionID != "" {
			return []agent.Event{{SessionID: m.SessionID}}, nil
		}
	case "assistant":
		return p.assistant(&m), nil
	case "user":
		return userBlocks(&m), nil
	case "rate_limit_event":
		return p.rateLimit(m.RateLimitInfo), nil
	case "result":
		return p.result(&m)
	case "control_response":
		if r := m.Response; r != nil && r.Subtype == "error" {
			return []agent.Event{logEv(domain.EventError, "control request failed: "+r.Error, nil)}, nil
		}
	}
	return nil, nil
}

func (p *parser) assistant(m *message) []agent.Event {
	var evs []agent.Event
	if m.Message != nil {
		var blocks []block
		_ = json.Unmarshal(m.Message.Content, &blocks)
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					evs = append(evs, logEv(domain.EventText, b.Text, nil))
				}
			case "thinking":
				if strings.TrimSpace(b.Thinking) != "" {
					evs = append(evs, logEv(domain.EventThinking, b.Thinking, nil))
				}
			case "tool_use":
				data := map[string]any{"id": b.ID, "name": b.Name, "input": b.Input}
				evs = append(evs, logEv(domain.EventToolCall, proc.ToolCallText(b.Name, b.Input), data))
			}
		}
	}
	if m.Error == "rate_limit" {
		evs = append(evs, p.limit()...)
	}
	return evs
}

func userBlocks(m *message) []agent.Event {
	if m.Message == nil {
		return nil
	}
	var blocks []block
	if json.Unmarshal(m.Message.Content, &blocks) != nil {
		return nil // plain string: an echo of our own prompt
	}
	var evs []agent.Event
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		text := proc.Truncate(contentText(b.Content), proc.MaxToolOutput)
		data := map[string]any{"toolUseId": b.ToolUseID, "isError": b.IsError}
		evs = append(evs, logEv(domain.EventToolResult, text, data))
	}
	return evs
}

// contentText flattens a tool_result content: a string or a list of blocks.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return string(raw)
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func (p *parser) rateLimit(info *rateLimitInfo) []agent.Event {
	if info == nil {
		return nil
	}
	if info.ResetsAt > 0 {
		p.resetsAt = time.Unix(info.ResetsAt, 0).UTC()
	}
	if info.RateLimitType != "" {
		p.limitTyp = info.RateLimitType
	}
	if info.Status == "rejected" {
		return p.limit()
	}
	u := &agent.Usage{Warning: info.Status == "allowed_warning", ResetsAt: p.resetsAt}
	if info.Utilization != nil {
		u.Utilization = *info.Utilization
	}
	return []agent.Event{{Usage: u}}
}

// limit reports the account's limit once per run.
func (p *parser) limit() []agent.Event {
	if p.limited {
		return nil
	}
	p.limited = true
	at := p.resetsAt
	if at.IsZero() || !at.After(p.now()) {
		at = p.now().Add(defaultLimitWait).UTC()
	}
	return []agent.Event{{Limit: &agent.Limit{ResetsAt: at, Type: p.limitTyp}}}
}

func (p *parser) result(m *message) ([]agent.Event, *result) {
	var evs []agent.Event
	if m.IsError && (apiStatus(m.APIErrorStatus) == 429 || limitText.MatchString(m.Result)) {
		evs = p.limit()
	}
	r := &result{summary: m.Result, cost: m.TotalCostUSD}
	if m.IsError && !p.limited {
		r.err = resultError(m)
	}
	return evs, r
}

func resultError(m *message) error {
	msg := m.Result
	if msg == "" {
		msg = strings.Join(m.Errors, "; ")
	}
	if msg == "" {
		msg = m.Subtype
	}
	if msg == "" {
		return errors.New("claude: turn failed")
	}
	return fmt.Errorf("claude: %s", msg)
}

func apiStatus(raw json.RawMessage) int {
	n, _ := strconv.Atoi(string(bytes.Trim(raw, `"`)))
	return n
}

func logEv(kind domain.EventKind, text string, data any) agent.Event {
	return agent.Event{Log: proc.NewLog(kind, text, data)}
}
