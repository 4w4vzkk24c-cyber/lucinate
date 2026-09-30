package openclaw

// Acceptance suite for forge card 1597 (LUCINATE-V1-01), work item W3
// (adapter side): the 4-step pre-send attachment validation and typed
// error paths.
//
// RED at base: backend.ChatSendParams (the type exists at base; the
// Attachments field does not) carries no Attachments field, so the fixture
// constructor fails each pin with a named reason. Post-W3 the pins reject
// exactly the mutants M13 (validation deleted), M14 (projection deleted),
// M16 (defaults fallback removed), and M15 (typed rejection swallowed).
//
// The "server never sees it" property is proven structurally: the client
// here has NO transport (nil), so any error that names a policy limit was
// produced by pre-send validation — reaching the wire would have panicked
// instead.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// w3WriteFile writes size bytes of deterministic content and stats it back.
func w3WriteFile(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	chunk := make([]byte, 32*1024)
	for i := range chunk {
		chunk[i] = byte(i % 251)
	}
	var written int64
	for written < size {
		n := int64(len(chunk))
		if remaining := size - written; remaining < n {
			n = remaining
		}
		if _, err := f.Write(chunk[:n]); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		written += n
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", name, err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != size {
		t.Fatalf("fixture %s stat mismatch: %v (size %d)", name, err, size)
	}
	return path
}

// w3ParamsWithAttachment builds backend.ChatSendParams carrying one
// attachment aimed at path. The Attachments field does not exist at base —
// it is discovered by reflection so this file compiles at base, and every
// pin fails there with the named reason instead of a compile error.
func w3ParamsWithAttachment(t *testing.T, path string) backend.ChatSendParams {
	t.Helper()
	params := backend.ChatSendParams{Message: "see attachment"}
	pv := reflect.ValueOf(&params).Elem()
	f := pv.FieldByName("Attachments")
	if !f.IsValid() || f.Kind() != reflect.Slice {
		t.Fatalf("W3: backend.ChatSendParams carries no Attachments field (W3 not implemented; M13 territory)")
	}
	elem := reflect.New(f.Type().Elem()).Elem()
	if !w3SetPathFields(elem, path) {
		t.Fatalf("W3: could not aim backend.Attachment at %s — no settable path string field on %s", path, f.Type().Elem())
	}
	f.Set(reflect.Append(f, elem))
	return pv.Interface().(backend.ChatSendParams)
}

// w3SetPathFields sets whichever path-ish/naming fields the builder defined
// on backend.Attachment; true when a path field actually took the value.
func w3SetPathFields(elem reflect.Value, path string) bool {
	setFirst := func(names ...string) bool {
		for _, n := range names {
			f := elem.FieldByName(n)
			if f.IsValid() && f.Kind() == reflect.String && f.CanSet() {
				f.SetString(path)
				return true
			}
		}
		return false
	}
	okPath := setFirst("Path", "FilePath", "LocalPath")
	setFirst("FileName", "Name")
	setFirst("MimeType", "MIMEType", "ContentType")
	return okPath
}

// w3CallSend sends through the real adapter, converting a nil-transport
// panic (the wire attempted without a connection) into an error so the
// assertions can distinguish "validation rejected" from "reached transport".
func w3CallSend(t *testing.T, b *Backend, params backend.ChatSendParams) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: reached the transport with no connection: %v", r)
		}
	}()
	_, err = b.ChatSend(context.Background(), "sess-1", params)
	return err
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// W3-AC2 / M13: an image over 6 MB is rejected pre-send with a typed error
// citing maxImageBytes and the actual size — the server never sees it.
func TestW3AC2_ImageOver6MBRejectedPreSend(t *testing.T) {
	path := w3WriteFile(t, t.TempDir(), "big.png", 7*1024*1024) // 7,340,032 B
	b := New(nil)

	err := w3CallSend(t, b, w3ParamsWithAttachment(t, path))
	if err == nil {
		t.Fatalf("W3-AC2: 7 MiB image sent without validation error (ban B8/M13)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "7340032") {
		t.Errorf("W3-AC2: typed error omits actual size 7340032: %v", err)
	}
	if !containsAny(msg, "6291456", "maxImageBytes", "6 MB", "6MiB", "6 MiB") {
		t.Errorf("W3-AC2: typed error omits the 6 MB image limit: %v", err)
	}
}

// W3-AC3 / M14: a 19,922,945 B file passes the 20 MB decoded check but
// fails the 4/3 base64 projection (26,563,927 > 26,214,400) — the
// rejection must show that math, proving the projection step ran.
func TestW3AC3_ProjectionNotDecodedSizeTripsTheRejection(t *testing.T) {
	path := w3WriteFile(t, t.TempDir(), "spill.bin", 19922945)
	b := New(nil)

	err := w3CallSend(t, b, w3ParamsWithAttachment(t, path))
	if err == nil {
		t.Fatalf("W3-AC3: 19 MiB+1 file accepted — oversize frame on the wire (projection deleted, M14)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "26563927") {
		t.Errorf("W3-AC3: typed error omits projected size 26563927 (projection step deleted or math unshown): %v", err)
	}
	if !strings.Contains(msg, "26214400") {
		t.Errorf("W3-AC3: typed error omits the 25 MiB cap 26214400: %v", err)
	}
}

// W3-AC5 / M14: boundary — projected size exactly at the cap is accepted,
// one byte over is rejected with the cap named. No outbound frame may
// exceed policy.maxPayload.
func TestW3AC5_NoFrameExceedsMaxPayloadAtTheBoundary(t *testing.T) {
	dir := t.TempDir()
	// decoded 19,660,800 -> projected 26,214,400 == cap: accepted.
	atCap := w3WriteFile(t, dir, "at-cap.bin", 19660800)
	// decoded 19,660,801 -> projected above cap: rejected.
	overCap := w3WriteFile(t, dir, "over-cap.bin", 19660801)
	b := New(nil)

	errAt := w3CallSend(t, b, w3ParamsWithAttachment(t, atCap))
	if errAt != nil && strings.Contains(errAt.Error(), "26214400") {
		t.Errorf("W3-AC5: projected size exactly at the cap was rejected: %v", errAt)
	}

	errOver := w3CallSend(t, b, w3ParamsWithAttachment(t, overCap))
	if errOver == nil {
		t.Fatalf("W3-AC5: projected size over the cap was accepted (oversize frame)")
	}
	if !strings.Contains(errOver.Error(), "26214400") {
		t.Errorf("W3-AC5: rejection does not name the 25 MiB cap 26214400: %v", errOver)
	}
}

// W3-AC6 / M16: with no hello-ok policy in play (client never connected)
// the documented defaults apply — 20 MB decoded / 6 MB image / 25 MiB
// frame — no hard failure, no zero-cap.
func TestW3AC6_PolicyAbsentAppliesDocumentedDefaults(t *testing.T) {
	dir := t.TempDir()
	bigImage := w3WriteFile(t, dir, "default-image.png", 7*1024*1024)
	bigOther := w3WriteFile(t, dir, "default-other.bin", 21*1024*1024) // 22,020,096 B
	b := New(nil)

	errImg := w3CallSend(t, b, w3ParamsWithAttachment(t, bigImage))
	if errImg == nil {
		t.Fatalf("W3-AC6: policy-absent image send skipped the 6 MB default ceiling")
	}
	if !containsAny(errImg.Error(), "6291456", "maxImageBytes", "6 MB", "6MiB", "6 MiB") {
		t.Errorf("W3-AC6: policy-absent image rejection does not name the 6 MB default: %v", errImg)
	}

	errOther := w3CallSend(t, b, w3ParamsWithAttachment(t, bigOther))
	if errOther == nil {
		t.Fatalf("W3-AC6: policy-absent 21 MiB send skipped the 20 MB default ceiling")
	}
	if !containsAny(errOther.Error(), "20971520", "maxBytes", "20 MB", "20MiB", "20 MiB") {
		t.Errorf("W3-AC6: policy-absent rejection does not name the 20 MB default: %v", errOther)
	}
}

// W3-AC4 / M15: a typed server rejection surfaces with its code AND
// message, driven against a minimal fake gateway speaking the protocol
// handshake (challenge -> connect -> hello-ok) then rejecting chat.send
// with a typed error payload. The fake stubs the GATEWAY; the unit under
// test is the lucinate adapter.
func TestW3AC4_TypedServerRejectionSurfacesCodeAndMessage(t *testing.T) {
	code := "payload_too_large"
	message := "attachment frame 26563927 exceeds maxPayload 26214400"
	gw := newFakeGateway(t, w3TypedErrorScript{code: code, message: message})
	defer gw.close()

	b := gw.backend(t)

	_, err := b.ChatSend(context.Background(), "sess-1", backend.ChatSendParams{Message: "hello"})
	if err == nil {
		t.Fatalf("W3-AC4: server typed rejection swallowed entirely")
	}
	msg := err.Error()
	if !strings.Contains(msg, code) {
		t.Errorf("W3-AC4: error dropped the typed code %q (generic failure, M15): %v", code, err)
	}
	if !strings.Contains(msg, message) {
		t.Errorf("W3-AC4: error dropped the server message %q: %v", message, err)
	}
}

// --- fake gateway ---------------------------------------------------------

// w3TypedErrorScript is the rejection the fake gateway answers chat.send with.
type w3TypedErrorScript struct {
	code    string
	message string
}

// fakeGateway is a minimal protocol-speaking WebSocket server.
type fakeGateway struct {
	srv    *httptest.Server
	script w3TypedErrorScript
	done   chan struct{}
}

func newFakeGateway(t *testing.T, script w3TypedErrorScript) *fakeGateway {
	t.Helper()
	g := &fakeGateway{script: script, done: make(chan struct{})}
	up := websocket.Upgrader{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(g.done)

		// 1. connect.challenge event.
		challenge, _ := json.Marshal(map[string]any{
			"type": "event", "event": "connect.challenge",
			"payload": map[string]any{"nonce": "n1597", "ts": time.Now().Unix()},
		})
		if err := conn.WriteMessage(websocket.TextMessage, challenge); err != nil {
			return
		}
		// 2. Request loop: echo the id, answer connect and chat.send. Every
	// RPC reply carries the response frame type the SDK router filters on.
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			id := req["id"]
			method, _ := req["method"].(string)
			var resp map[string]any
			switch method {
			case "connect":
				resp = map[string]any{"id": id, "type": "res", "ok": true, "payload": map[string]any{
					"type": "hello-ok", "protocol": 4,
					"server":   map[string]any{"version": "2026.9.4", "connId": "t"},
					"features": map[string]any{}, "snapshot": map[string]any{},
					"policy": map[string]any{},
				}}
			case "chat.send":
				resp = map[string]any{"id": id, "type": "res", "ok": false, "error": map[string]any{
					"code": g.script.code, "message": g.script.message,
				}}
			default:
				resp = map[string]any{"id": id, "type": "res", "ok": true, "payload": map[string]any{}}
			}
			out, _ := json.Marshal(resp)
			if err := conn.WriteMessage(websocket.TextMessage, out); err != nil {
				return
			}
		}
	}))
	g.srv.URL = "ws" + strings.TrimPrefix(g.srv.URL, "http")
	return g
}

func (g *fakeGateway) close() {
	g.srv.Close()
	select {
	case <-g.done:
	default:
	}
}

// backend connects a lucinate client (and thus the adapter) to the fake
// gateway with the identity store isolated to a temp dir.
func (g *fakeGateway) backend(t *testing.T) *Backend {
	t.Helper()
	// Isolate the identity store to a temp dir, restoring whatever the
	// process had set before.
	prev, prevErr := config.DataDir()
	config.SetDataDir(t.TempDir())
	t.Cleanup(func() { config.SetDataDir(prev) })
	_ = prevErr
	cfg, err := config.New(g.srv.URL)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect to fake gateway: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return New(c)
}
