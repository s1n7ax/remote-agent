package claude

import (
	"testing"
	"time"
)

func TestParserLimitFallsBackToAnHour(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := parser{now: func() time.Time { return now }}
	evs, res := p.handle([]byte(`{"type":"result","is_error":true,"result":"You've hit your session limit · resets 3pm"}`))
	if res == nil || res.err != nil {
		t.Fatalf("result = %+v", res)
	}
	if len(evs) != 1 || evs[0].Limit == nil || !evs[0].Limit.ResetsAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("events = %+v", evs)
	}
	// Reported once per run.
	if evs, _ := p.handle([]byte(`{"type":"assistant","message":{"content":[]},"error":"rate_limit"}`)); len(evs) != 0 {
		t.Errorf("second limit emitted: %+v", evs)
	}
}

func TestParserLimitUsesLastResetsAt(t *testing.T) {
	now := time.Unix(1000, 0)
	p := parser{now: func() time.Time { return now }}
	p.handle([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":5000,"rateLimitType":"five_hour","utilization":0.5}}`))
	evs, _ := p.handle([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]},"error":"rate_limit"}`))
	if len(evs) != 2 || evs[1].Limit == nil || evs[1].Limit.ResetsAt.Unix() != 5000 || evs[1].Limit.Type != "five_hour" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestParserIgnoresNoise(t *testing.T) {
	p := parser{now: time.Now}
	for _, line := range []string{
		`{"type":"system","subtype":"compact_boundary"}`,
		`{"type":"user","message":{"role":"user","content":"echo"}}`,
		`{"type":"stream_event"}`,
	} {
		if evs, res := p.handle([]byte(line)); len(evs) != 0 || res != nil {
			t.Errorf("%s: got %+v %+v", line, evs, res)
		}
	}
	if evs, _ := p.handle([]byte(`not json`)); len(evs) != 1 || evs[0].Log == nil {
		t.Errorf("garbage should log an error: %+v", evs)
	}
}
