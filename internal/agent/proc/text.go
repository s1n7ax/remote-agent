package proc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

// MaxToolOutput bounds the text of a tool-result log line.
const MaxToolOutput = 2048

// maxToolInput bounds the arguments shown on a tool-call log line.
const maxToolInput = 300

// Truncate shortens s to at most max bytes (on a rune boundary) and notes how
// much was cut.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s… (%d bytes truncated)", s[:cut], len(s)-cut)
}

// ToolCallText renders "name {compact args}" for a tool-call log line.
func ToolCallText(name string, args json.RawMessage) string {
	if len(args) == 0 || string(args) == "null" || string(args) == "{}" {
		return name
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, args); err != nil {
		return name
	}
	return name + " " + Truncate(buf.String(), maxToolInput)
}

// hostEnv lists the host variables a child inherits; everything else (the
// app's own secrets included) is left out.
var hostEnv = []string{
	"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TERM",
	"SSL_CERT_FILE", "NIX_SSL_CERT_FILE", "SSL_CERT_DIR",
}

// Env builds a child environment: the allowlisted host variables, then base,
// then extra (later wins). Empty values in base are skipped; keys in drop are
// removed. The result is sorted.
func Env(base, extra map[string]string, drop ...string) []string {
	m := map[string]string{}
	for _, k := range hostEnv {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	for k, v := range base {
		if v != "" {
			m[k] = v
		}
	}
	for k, v := range extra {
		m[k] = v
	}
	for _, k := range drop {
		delete(m, k)
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Limiter is a token bucket that keeps a noisy stderr from flooding the run
// log.
type Limiter struct {
	mu      sync.Mutex
	tokens  float64
	burst   float64
	rate    float64 // tokens per second
	last    time.Time
	dropped int
}

// NewLimiter allows burst lines at once and rate lines per second after.
func NewLimiter(burst int, rate float64) *Limiter {
	return &Limiter{tokens: float64(burst), burst: float64(burst), rate: rate, last: time.Now()}
}

// Allow reports whether a line may be logged now, counting the ones that may
// not.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	if l.tokens < 1 {
		l.dropped++
		return false
	}
	l.tokens--
	return true
}

// Dropped returns how many lines Allow refused.
func (l *Limiter) Dropped() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}
