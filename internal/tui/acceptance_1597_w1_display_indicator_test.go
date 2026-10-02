package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
)

func TestCleanSessionDisplayKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain key", "my-session", "my-session"},
		{"agent main prefix", "agent:main:dashboard:ec828e06-b92d-4fbf-8be7-608f664a8f20", "ec828e06-b92d-4fbf-8be7-608f664a8f20"},
		{"agent scout prefix", "agent:scout:12345", "12345"},
		{"agent with dashboard only", "agent:main:dashboard:alpha", "alpha"},
		{"dashboard only prefix", "dashboard:subagent:999", "subagent:999"},
		{"empty string", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanSessionDisplayKey(tc.in)
			if got != tc.want {
				t.Errorf("cleanSessionDisplayKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSessionDelegate_ActivityIndicatorRender(t *testing.T) {
	trueVal := true
	falseVal := false

	cases := []struct {
		name      string
		activeRun *bool
		wantMark  string
	}{
		{"active run true", &trueVal, "♛"},
		{"active run nil unknown", nil, "♕"},
		// Amended 2026-10-02: this case asserted "  ", which every row
		// contains, so it pinned nothing — and the gateway always sends
		// false for idle, so idle sessions rendered with no glyph at all.
		{"active run false idle", &falseVal, "♕"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := sessionItem{
				key:          "test-key",
				title:        "Session Title",
				hasActiveRun: tc.activeRun,
			}
			var buf strings.Builder
			d := sessionDelegate{}
			l := list.New(nil, d, 100, 20)
			d.Render(&buf, l, 0, item)
			out := buf.String()
			if !strings.Contains(out, tc.wantMark) {
				t.Errorf("Render() missing expected marker %q; output:\n%s", tc.wantMark, out)
			}
		})
	}
}

func TestSessionDelegate_SubagentIconRender(t *testing.T) {
	trueVal := true

	t.Run("subagent active uses pawn", func(t *testing.T) {
		item := sessionItem{
				key:          "agent:main:subagent:a68db7ea-0589-4052-a8d7-704cb6b04350",
				title:        "Subagent Task",
				hasActiveRun: &trueVal,
			}
			var buf strings.Builder
		d := sessionDelegate{}
		l := list.New(nil, d, 100, 20)
		d.Render(&buf, l, 0, item)
		out := buf.String()
		if !strings.Contains(out, "♟") {
			t.Errorf("Subagent active render missing pawn ♟; output:\n%s", out)
		}
		if strings.Contains(out, "♛") {
			t.Errorf("Subagent active render should NOT contain king ♛; output:\n%s", out)
		}
	})

	t.Run("subagent unknown uses outline pawn", func(t *testing.T) {
		item := sessionItem{
				key:          "agent:main:subagent:a68db7ea-0589-4052-a8d7-704cb6b04350",
				title:        "Subagent Task",
				hasActiveRun: nil,
			}
		var buf strings.Builder
		d := sessionDelegate{}
		l := list.New(nil, d, 100, 20)
		d.Render(&buf, l, 0, item)
		out := buf.String()
		if !strings.Contains(out, "♙") {
			t.Errorf("Subagent unknown render missing outline pawn ♙; output:\n%s", out)
		}
	})

	t.Run("real agent active uses king not pawn", func(t *testing.T) {
		item := sessionItem{
				key:          "agent:main:dashboard:ec828e06-b92d-4fbf-8be7-608f664a8f20",
				title:        "Real Session",
				hasActiveRun: &trueVal,
			}
			var buf strings.Builder
		d := sessionDelegate{}
		l := list.New(nil, d, 100, 20)
		d.Render(&buf, l, 0, item)
		out := buf.String()
		if !strings.Contains(out, "♛") {
			t.Errorf("Real agent active render missing king ♛; output:\n%s", out)
		}
		if strings.Contains(out, "♟") {
			t.Errorf("Real agent active render should NOT contain pawn ♟; output:\n%s", out)
		}
	})
}
