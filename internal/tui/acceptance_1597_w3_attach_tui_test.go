package tui

// Acceptance suite for forge card 1597 (LUCINATE-V1-01), work item W3
// (lucinate side): /attach + ctrl+a staging, chips above the input, and the
// send path carrying attachments in backend.ChatSendParams.
//
// RED at base: /attach does not exist in the command table and
// backend.ChatSendParams has no Attachments field, so the staging and
// carry pins fail. The post-W3 field is probed with reflection — no
// compile-time reference to symbols that do not exist at base. The
// oversize / projection / defaults error paths are pinned at the adapter
// boundary in internal/backend/openclaw/acceptance_1597_attach_validation_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// w3SendProbe records the ChatSendParams each send carries.
type w3SendProbe struct {
	fakeBackend
	mu   sync.Mutex
	sent []backend.ChatSendParams
}

func (f *w3SendProbe) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	f.mu.Lock()
	f.sent = append(f.sent, params)
	f.mu.Unlock()
	return f.fakeBackend.ChatSend(ctx, sessionKey, params)
}

// w3SentLast returns the most recent recorded params.
func (f *w3SendProbe) w3SentLast() (backend.ChatSendParams, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return backend.ChatSendParams{}, false
	}
	return f.sent[len(f.sent)-1], true
}

// w3NewChat builds a chatModel on the probe with viewport sized.
func w3NewChat(probe *w3SendProbe) chatModel {
	m := newChatModel(probe, "sess-1", "agent-1", "Scout", "model-1", config.DefaultPreferences(), false, "", "", false)
	m.viewport = viewport.New()
	m.setSize(120, 40)
	return m
}

// w3RunCmd executes one command and feeds its message back into the chat
// model — the hop the program loop would perform. Batch results expand.
func w3RunCmd(m chatModel, cmd tea.Cmd) chatModel {
	pending := []tea.Cmd{cmd}
	for i := 0; i < 50 && len(pending) > 0; i++ {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, bm...)
			continue
		}
		next, nc := m.Update(msg)
		m = next
		if nc != nil {
			pending = append(pending, nc)
		}
	}
	return m
}

// w3TypeString feeds literal characters as key presses.
func w3TypeString(m chatModel, s string) chatModel {
	for _, r := range s {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next
	}
	return m
}

// w3CarriedAttachments reflects the Attachments field off recorded send
// params; ok=false when the field does not exist (base state).
func w3CarriedAttachments(params backend.ChatSendParams) (reflect.Value, bool) {
	field := reflect.ValueOf(params).FieldByName("Attachments")
	if !field.IsValid() || field.Kind() != reflect.Slice {
		return reflect.Value{}, false
	}
	return field, true
}

// W3 adapter + TUI: /attach <path> stages a chip above the input and the
// next send carries it in ChatSendParams.Attachments. Red at base: the
// command is unknown (no chip) and the params field absent.
func TestW3_AttachStagesChipAndSendCarriesAttachments(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "w3.png")
	if err := os.WriteFile(png, []byte("pretend png bytes"), 0600); err != nil {
		t.Fatalf("write attachment fixture: %v", err)
	}

	probe := &w3SendProbe{}
	m := w3NewChat(probe)

	handled, cmd := m.handleSlashCommand("/attach " + png)
	if !handled {
		t.Fatalf("W3: /attach not handled by the command table (command missing)")
	}
	m = w3RunCmd(m, cmd)

	if !strings.Contains(m.View(), "w3.png") {
		t.Errorf("W3: staged attachment does not render as a chip above the input (no %q in view)", "w3.png")
	}

	m = w3RunCmd(m, m.sendMessage("see attachment"))
	sent, ok := probe.w3SentLast()
	if !ok {
		t.Fatalf("W3: send did not reach the backend")
	}
	atts, ok := w3CarriedAttachments(sent)
	if !ok {
		t.Fatalf("W3: backend.ChatSendParams carries no Attachments field (W3 not implemented)")
	}
	if atts.Len() != 1 {
		t.Fatalf("W3: send carried %d attachments, want 1", atts.Len())
	}
}

// W3 adapter + TUI: ctrl+a opens the path prompt and stages the typed path
// the same as /attach. Red at base: ctrl+a is unbound, so the typed path
// lands in the composer as message text and no attachment is carried.
func TestW3_CtrlAPathPromptStagesAttachment(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "ctrla.png")
	if err := os.WriteFile(png, []byte("bytes"), 0600); err != nil {
		t.Fatalf("write attachment fixture: %v", err)
	}

	probe := &w3SendProbe{}
	m := w3NewChat(probe)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m = next
	m = w3TypeString(m, png)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next

	if !strings.Contains(m.View(), "ctrla.png") {
		t.Errorf("W3: ctrl+a staged attachment does not render as a chip above the input")
	}
	if got := m.textarea.Value(); strings.Contains(got, "ctrla.png") {
		t.Errorf("W3: ctrl+a path left in the composer (no prompt flow captured it); composer=%q", got)
	}

	m = w3RunCmd(m, m.sendMessage("with ctrl+a attachment"))
	sent, ok := probe.w3SentLast()
	if !ok {
		t.Fatalf("W3: send did not reach the backend")
	}
	atts, ok := w3CarriedAttachments(sent)
	if !ok {
		t.Fatalf("W3: backend.ChatSendParams carries no Attachments field (W3 not implemented)")
	}
	if atts.Len() != 1 {
		t.Fatalf("W3: ctrl+a-staged attachment not carried on send (got %d)", atts.Len())
	}
}

// W3-AC4 surface half: a typed server rejection reaches the visible
// transcript with BOTH its code and message intact. This pins the UI
// pass-through (retained plumbing, green at base); the adapter half — not
// swallowing the typed error at the backend boundary — is pinned against a
// live-shaped gateway in the adapter acceptance file.
func TestW3AC4_TypedServerRejectionSurfacesCodeAndMessage(t *testing.T) {
	probe := &w3SendProbe{}
	m := w3NewChat(probe)

	m.notifyError("payload_too_large: frame 26563927 exceeds maxPayload 26214400")
	view := m.View()
	if !strings.Contains(view, "payload_too_large") {
		t.Errorf("W3-AC4: rejection code dropped from the visible surface")
	}
	if !strings.Contains(view, "26214400") {
		t.Errorf("W3-AC4: rejection message detail dropped from the visible surface")
	}
}
