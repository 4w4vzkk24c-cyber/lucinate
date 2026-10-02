package client

import (
	"encoding/json"
	"testing"
)

// The gateway validates sessions.patch against a closed schema: a misnamed
// or extra field is rejected outright, so the body's exact shape is the
// contract.
func TestSessionArchiveParams_WireShape(t *testing.T) {
	got, err := json.Marshal(sessionArchiveParams{Key: "agent:main:dashboard:abc", Archived: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"key":"agent:main:dashboard:abc","archived":true}`
	if string(got) != want {
		t.Errorf("sessions.patch archive body = %s, want %s", got, want)
	}
}
