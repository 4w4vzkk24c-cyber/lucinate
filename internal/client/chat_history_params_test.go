package client

import (
	"encoding/json"
	"testing"
)

// Without maxChars the gateway cuts every history text at 8,000 characters
// and appends "...(truncated)...", so a long reply that streamed in whole is
// replaced by a cut copy at the next history refresh.
func TestChatHistoryParams_AsksForUncutMessages(t *testing.T) {
	got, err := json.Marshal(chatHistoryParams("agent:main:dashboard:abc", 50))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"sessionKey":"agent:main:dashboard:abc","limit":50,"maxChars":500000}`
	if string(got) != want {
		t.Errorf("chat.history params = %s, want %s", got, want)
	}
}

// The gateway's schema rejects a maxChars above its ceiling, which would
// fail every history load.
func TestChatHistoryParams_StaysWithinTheGatewayCeiling(t *testing.T) {
	const gatewayCeiling = 500_000 // ChatHistoryParamsSchema maxChars maximum, OpenClaw 2026.9.4
	if historyMaxChars > gatewayCeiling {
		t.Errorf("historyMaxChars is %d, above the gateway's ceiling of %d", historyMaxChars, gatewayCeiling)
	}
	if historyMaxChars < 100_000 {
		t.Errorf("historyMaxChars is %d; long replies would still be cut", historyMaxChars)
	}
}
