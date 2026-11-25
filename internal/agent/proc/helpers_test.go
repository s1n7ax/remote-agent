package proc

import (
	"os"
	"testing"

	"github.com/s1n7ax/remote-agent/internal/agent"
)

func eventCost(c float64) agent.Event { return agent.Event{Cost: c} }

func pathEnv(t *testing.T) string {
	t.Helper()
	return os.Getenv("PATH")
}
