package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for the claude CLI: with
// FAKE_CLAUDE_MODE set it speaks a scripted stream-json dialogue.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_CLAUDE_MODE"); mode != "" {
		os.Exit(fakeCLI(mode))
	}
	os.Exit(m.Run())
}

func fakeCLI(mode string) int {
	if rec := os.Getenv("FAKE_CLAUDE_RECORD"); rec != "" {
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ()})
		_ = os.WriteFile(rec, b, 0o600)
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(nil, 1<<20)
	out := func(s string) { fmt.Println(s) }
	next := func() map[string]any {
		if !in.Scan() {
			return nil
		}
		var m map[string]any
		_ = json.Unmarshal(in.Bytes(), &m)
		return m
	}
	drain := func() int {
		for in.Scan() {
		}
		return 0
	}
	first := next()
	if first == nil {
		return 9
	}
	out(`{"type":"system","subtype":"init","session_id":"sess-1","model":"m"}`)
	switch mode {
	case "ok":
		long := strings.Repeat("x", 5000)
		out(`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"hello"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}}]},"parent_tool_use_id":null}`)
		out(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"` + long + `"}],"is_error":false}]}}`)
		out(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":2000000000,"rateLimitType":"five_hour","utilization":0.91}}`)
		out(`{"type":"result","subtype":"success","is_error":false,"result":"all done","total_cost_usd":0.25}`)
		return drain()
	case "error":
		out(`{"type":"result","subtype":"success","is_error":true,"result":"Not logged in","total_cost_usd":0,"api_error_status":null}`)
		return drain()
	case "limit":
		out(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":2000000000,"rateLimitType":"seven_day"}}`)
		out(`{"type":"assistant","message":{"content":[{"type":"text","text":"You've hit your weekly limit"}]},"error":"rate_limit"}`)
		out(`{"type":"result","subtype":"success","is_error":true,"result":"You've hit your weekly limit","api_error_status":429}`)
		return drain()
	case "interrupt":
		out(`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`)
		for m := next(); m != nil; m = next() {
			switch m["type"] {
			case "control_request":
				out(`{"type":"control_response","response":{"subtype":"success","request_id":"x"}}`)
				out(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","total_cost_usd":0.1}`)
			case "user":
				msg := m["message"].(map[string]any)
				b, _ := json.Marshal("echo: " + msg["content"].(string) + " prio=" + fmt.Sprint(m["priority"]))
				out(`{"type":"assistant","message":{"content":[{"type":"text","text":` + string(b) + `}]}}`)
				out(`{"type":"result","subtype":"success","is_error":false,"result":"resumed","total_cost_usd":0.3}`)
			}
		}
		return 0
	case "crash":
		fmt.Fprintln(os.Stderr, "boom")
		return 3
	case "stubborn":
		signal.Ignore(syscall.SIGINT)
		time.Sleep(time.Hour) // only SIGKILL ends this
		return 7
	case "hang":
		return drain()
	}
	return 8
}
