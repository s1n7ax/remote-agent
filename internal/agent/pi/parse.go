package pi

// RPC protocol of `pi --mode rpc`, verified against pi-coding-agent 1.6.0
// (nixpkgs#pi-coding-agent: docs/rpc.md, docs/rpc-commands.md, docs/json.md,
// docs/message-types.md, docs/rpc-extension-ui.md and dist/modes/rpc/
// rpc-mode.js):
//
//   - JSONL on stdin/stdout, split on LF only; stderr is diagnostics.
//   - Commands: {"id","type":"prompt","message"} (a run must be idle, else
//     streamingBehavior is required), {"type":"steer","message"} (queued
//     for after the current tool calls), {"type":"abort"} (waits for idle),
//     {"type":"get_state"} → data.sessionId / data.sessionFile.
//   - Every command gets {"type":"response","command","success","error"?,
//     "data"?}; a prompt's data.disposition is started|queued|handled.
//   - Events: message_update {assistantMessageEvent:{type:"text_delta",…}}
//     (delta-only, so we log the authoritative message_end instead);
//     message_end {message:{role:"assistant",content:[{type:"text",text} |
//     {type:"thinking",thinking} | {type:"toolCall",…}],usage:{cost:{total}},
//     stopReason:"stop"|"toolUse"|"error"|"aborted"|…,errorMessage?}};
//     tool_execution_start {toolCallId,toolName,args}; tool_execution_end
//     {toolCallId,toolName,result:{content:[…]},isError}; agent_end
//     {messages,willRetry}; agent_settled {aborted} (no more automatic work:
//     the end of a run); auto_retry_end {success,finalError?};
//     extension_error {error}.
//   - Extension dialogs (extension_ui_request with method select|confirm|
//     input|editor) block until an extension_ui_response {id,cancelled:true}.
//   - Closing stdin requests an orderly shutdown.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/s1n7ax/remote-agent/internal/agent"
	"github.com/s1n7ax/remote-agent/internal/agent/proc"
	"github.com/s1n7ax/remote-agent/internal/domain"
)

// promptID tags the first prompt so its response can be told apart.
const promptID = "r1"

// stateID tags the get_state request that reports the session.
const stateID = "state"

type record struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// response
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`

	// message_end
	Message *piMessage `json:"message"`

	// tool_execution_*
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     *toolResult     `json:"result"`
	IsError    bool            `json:"isError"`

	// agent_settled, auto_retry_end, extension_ui_request
	Aborted    bool   `json:"aborted"`
	FinalError string `json:"finalError"`
	Method     string `json:"method"`
}

type piMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Cost struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

// outcome is what a record means for the run as a whole.
type outcome int

const (
	keepGoing outcome = iota
	settled           // agent_settled: the run is over (or interrupted)
	failed            // the first prompt was rejected
	handled           // the first prompt ran no agent
)

// parser turns stdout records into events. It is not safe for concurrent use.
type parser struct {
	lastText string // last assistant text, the run's summary
	lastErr  string // error of the last assistant message, if it failed
	reply    []any  // records to write back (dialog cancellations)
}

func (p *parser) handle(line []byte) ([]agent.Event, outcome, error) {
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return []agent.Event{logEv(domain.EventError, "unparsed output: "+proc.Truncate(string(line), proc.MaxToolOutput), nil)}, keepGoing, nil
	}
	switch r.Type {
	case "response":
		return p.response(&r)
	case "message_end":
		return p.messageEnd(r.Message), keepGoing, nil
	case "tool_execution_start":
		data := map[string]any{"id": r.ToolCallID, "name": r.ToolName, "input": r.Args}
		return []agent.Event{logEv(domain.EventToolCall, proc.ToolCallText(r.ToolName, r.Args), data)}, keepGoing, nil
	case "tool_execution_end":
		text := ""
		if r.Result != nil {
			text = blocksText(r.Result.Content)
		}
		data := map[string]any{"toolUseId": r.ToolCallID, "isError": r.IsError}
		return []agent.Event{logEv(domain.EventToolResult, proc.Truncate(text, proc.MaxToolOutput), data)}, keepGoing, nil
	case "agent_settled":
		return nil, settled, nil
	case "auto_retry_end":
		if !r.Success && r.FinalError != "" {
			return []agent.Event{logEv(domain.EventError, "retries failed: "+r.FinalError, nil)}, keepGoing, nil
		}
	case "compaction_start":
		return []agent.Event{logEv(domain.EventSystem, "compacting context", nil)}, keepGoing, nil
	case "extension_error":
		return []agent.Event{logEv(domain.EventError, "extension: "+r.Error, nil)}, keepGoing, nil
	case "extension_ui_request":
		switch r.Method {
		case "select", "confirm", "input", "editor":
			// Nobody can answer a dialog in a headless run.
			p.reply = append(p.reply, map[string]any{"type": "extension_ui_response", "id": r.ID, "cancelled": true})
		}
	}
	return nil, keepGoing, nil
}

func (p *parser) response(r *record) ([]agent.Event, outcome, error) {
	if !r.Success {
		err := fmt.Errorf("pi: %s: %s", r.Command, r.Error)
		if r.ID == promptID {
			return nil, failed, err
		}
		return []agent.Event{logEv(domain.EventError, err.Error(), nil)}, keepGoing, nil
	}
	switch r.ID {
	case promptID:
		var d struct {
			Disposition string `json:"disposition"`
		}
		_ = json.Unmarshal(r.Data, &d)
		if d.Disposition == "handled" {
			return nil, handled, nil
		}
	case stateID:
		var d struct {
			SessionID   string `json:"sessionId"`
			SessionFile string `json:"sessionFile"`
		}
		_ = json.Unmarshal(r.Data, &d)
		// The file is what --session takes to resume; an in-memory session
		// has only the id.
		id := d.SessionFile
		if id == "" {
			id = d.SessionID
		}
		if id != "" {
			return []agent.Event{{SessionID: id}}, keepGoing, nil
		}
	}
	return nil, keepGoing, nil
}

func (p *parser) messageEnd(m *piMessage) []agent.Event {
	if m == nil || m.Role != "assistant" {
		return nil
	}
	var evs []agent.Event
	var blocks []contentBlock
	_ = json.Unmarshal(m.Content, &blocks)
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				evs = append(evs, logEv(domain.EventThinking, b.Thinking, nil))
			}
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				evs = append(evs, logEv(domain.EventText, b.Text, nil))
				texts = append(texts, b.Text)
			}
		}
	}
	if len(texts) > 0 {
		p.lastText = strings.Join(texts, "\n")
	}
	switch m.StopReason {
	case "error":
		p.lastErr = m.ErrorMessage
		if p.lastErr == "" {
			p.lastErr = "model call failed"
		}
		evs = append(evs, logEv(domain.EventError, p.lastErr, nil))
	default:
		p.lastErr = ""
	}
	if m.Usage != nil && m.Usage.Cost.Total != 0 {
		evs = append(evs, agent.Event{Cost: m.Usage.Cost.Total})
	}
	return evs
}

// runErr is the run's error once it settled.
func (p *parser) runErr() error {
	if p.lastErr == "" {
		return nil
	}
	return errors.New("pi: " + p.lastErr)
}

func blocksText(blocks []contentBlock) string {
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

func logEv(kind domain.EventKind, text string, data any) agent.Event {
	return agent.Event{Log: proc.NewLog(kind, text, data)}
}
