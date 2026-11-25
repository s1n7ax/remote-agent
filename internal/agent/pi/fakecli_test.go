package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestMain lets the test binary stand in for pi: with FAKE_PI_MODE set it
// speaks a scripted RPC dialogue.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_PI_MODE"); mode != "" {
		os.Exit(fakePi(mode))
	}
	os.Exit(m.Run())
}

func assistant(text string, cost float64, stop string) string {
	b, _ := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{
		"role":       "assistant",
		"content":    []map[string]string{{"type": "text", "text": text}},
		"usage":      map[string]any{"cost": map[string]any{"total": cost}},
		"stopReason": stop,
	}})
	return string(b)
}

func fakePi(mode string) int {
	if rec := os.Getenv("FAKE_PI_RECORD"); rec != "" {
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ()})
		_ = os.WriteFile(rec, b, 0o600)
	}
	in := bufio.NewScanner(os.Stdin)
	out := func(s string) { fmt.Println(s) }
	for in.Scan() {
		var c map[string]any
		_ = json.Unmarshal(in.Bytes(), &c)
		id, _ := c["id"].(string)
		msg, _ := c["message"].(string)
		switch c["type"] {
		case "get_state":
			out(`{"id":"` + id + `","type":"response","command":"get_state","success":true,"data":{"sessionId":"abc","sessionFile":"/home/x/.pi/s.jsonl"}}`)
		case "prompt":
			if mode == "reject" {
				out(`{"id":"` + id + `","type":"response","command":"prompt","success":false,"error":"No API key for openrouter"}`)
				continue
			}
			out(`{"id":"` + id + `","type":"response","command":"prompt","success":true,"data":{"disposition":"started"}}`)
			out(`{"type":"agent_start"}`)
			out(`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"par"}}`)
			switch {
			case mode == "ok":
				out(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"looking"},{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"ls"}}],"usage":{"cost":{"total":0.01}},"stopReason":"toolUse"}}`)
				out(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"ls"}}`)
				out(`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[{"type":"text","text":"a.go"}],"details":{}},"isError":false}`)
				out(`{"type":"extension_ui_request","id":"u1","method":"confirm","title":"ok?"}`)
				answer := "dialog unanswered"
				for in.Scan() {
					var r map[string]any
					_ = json.Unmarshal(in.Bytes(), &r)
					if r["type"] == "get_state" {
						out(`{"id":"state","type":"response","command":"get_state","success":true,"data":{"sessionId":"abc","sessionFile":"/home/x/.pi/s.jsonl"}}`)
					}
					if r["type"] == "extension_ui_response" && r["id"] == "u1" && r["cancelled"] == true {
						answer = "final answer"
						break
					}
				}
				out(assistant(answer, 0.02, "stop"))
				out(`{"type":"agent_end","messages":[],"willRetry":false}`)
				out(`{"type":"agent_settled","aborted":false}`)
			case mode == "error":
				out(`{"type":"message_end","message":{"role":"assistant","content":[],"usage":{"cost":{"total":0}},"stopReason":"error","errorMessage":"402 insufficient credits"}}`)
				out(`{"type":"agent_settled","aborted":false}`)
			case mode == "interrupt" && id == "r1":
				out(assistant("working", 0.01, "toolUse"))
			default: // interrupt mode, prompt after the abort
				out(assistant("echo: "+msg, 0.03, "stop"))
				out(`{"type":"agent_settled","aborted":false}`)
			}
		case "steer":
			out(`{"type":"response","command":"steer","success":true,"data":{"disposition":"queued"}}`)
		case "abort":
			out(assistant("", 0, "aborted"))
			out(`{"type":"agent_settled","aborted":true}`)
			out(`{"type":"response","command":"abort","success":true}`)
		}
	}
	return 0
}
