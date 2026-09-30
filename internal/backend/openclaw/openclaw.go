// Package openclaw adapts the existing OpenClaw gateway client to the
// backend.Backend interface. The adapter is a thin pass-through —
// every TUI call site that used to hold a *client.Client now holds a
// backend.Backend, and the OpenClaw concrete type is recovered via
// type assertion when the TUI needs gateway-only affordances
// (/status, !!, /compact, /think, /stats, device-token auth recovery).
package openclaw

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
)

// Backend wraps a *client.Client. Constructed by the embedder's
// BackendFactory when the picked connection has type=openclaw.
type Backend struct {
	client *client.Client

	// catalogSent tracks per-session whether the skill catalog has
	// been delivered to the gateway already. The gateway parses
	// System:-prefixed lines into a session-level system block and
	// retains them across turns, so we only need to deliver the
	// catalog with the first user message per session.
	mu          sync.Mutex
	catalogSent map[string]bool
}

// New wraps the given client. The caller still owns Connect / Close
// indirectly — both are forwarded through the Backend interface so
// the connection driver in app/app.go can drive the lifecycle without
// caring about the concrete type.
func New(c *client.Client) *Backend {
	return &Backend{
		client:      c,
		catalogSent: map[string]bool{},
	}
}

// Client exposes the underlying gateway client for tests and for the
// few places (auth-modal sub-state, TUI capability assertions) that
// still need the OpenClaw-specific surface.
func (b *Backend) Client() *client.Client { return b.client }

func (b *Backend) Connect(ctx context.Context) error { return b.client.Connect(ctx) }
func (b *Backend) Close() error                      { return b.client.Close() }
func (b *Backend) Events() <-chan protocol.Event     { return b.client.Events() }

func (b *Backend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	b.client.Supervise(ctx, notify)
}

// --- SessionsSubscriber ---

// SessionsSubscribe subscribes this connection to global sessions.changed
// events so the sessions sidebar refreshes live. The subscription is
// connection-scoped: it dies with the transport, so every reconnect must
// re-issue it (the TUI binds that to the Supervise connected-transition).
func (b *Backend) SessionsSubscribe(ctx context.Context) (json.RawMessage, error) {
	gw := b.client.GW()
	if gw == nil {
		return nil, client.ErrNotConnected
	}
	return gw.SessionsSubscribe(ctx)
}

func (b *Backend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	return b.client.ListAgents(ctx)
}

func (b *Backend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return b.client.CreateAgent(ctx, params.Name, params.Workspace)
}

func (b *Backend) DeleteAgent(ctx context.Context, params backend.DeleteAgentParams) error {
	return b.client.DeleteAgent(ctx, params.AgentID, params.DeleteFiles)
}

func (b *Backend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	return b.client.SessionsList(ctx, agentID)
}

func (b *Backend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	return b.client.CreateSession(ctx, agentID, key)
}

func (b *Backend) SessionDelete(ctx context.Context, sessionKey string) error {
	return b.client.SessionDelete(ctx, sessionKey)
}

func (b *Backend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	message := params.Message
	if catalog := b.takePendingCatalog(sessionKey, params.Skills); catalog != "" {
		message = catalog + "\n" + message
	}
	if len(params.Attachments) == 0 {
		return b.client.ChatSend(ctx, sessionKey, message, params.IdempotencyKey)
	}
	attachments, err := b.buildAttachments(params.Attachments)
	if err != nil {
		return nil, err
	}
	gw := b.client.GW()
	if gw == nil {
		return nil, client.ErrNotConnected
	}
	return gw.ChatSend(ctx, protocol.ChatSendParams{
		SessionKey:     sessionKey,
		Message:        message,
		IdempotencyKey: params.IdempotencyKey,
		Attachments:    attachments,
	})
}

// buildAttachments turns staged local paths into validated protocol
// attachments: stat every file first (never stream-unread files), run
// the gateway's 4-step pre-send check via the SDK against the live
// hello policy (connection-time snapshot; a reconnect re-reads it),
// and only then read + base64-encode. Every rejection is typed and
// names the ceiling and the actual size — the server never sees an
// oversize frame.
func (b *Backend) buildAttachments(atts []backend.Attachment) ([]protocol.ChatAttachment, error) {
	var policy *protocol.HelloPolicy
	if b.client != nil {
		if gw := b.client.GW(); gw != nil {
			if hello := gw.Hello(); hello != nil {
				p := hello.Policy
				policy = &p
			}
		}
	}
	out := make([]protocol.ChatAttachment, 0, len(atts))
	for _, att := range atts {
		if strings.TrimSpace(att.Path) == "" {
			return nil, fmt.Errorf("attachment: empty path")
		}
		// stat BEFORE reading (B9): the size drives validation.
		fi, err := os.Stat(att.Path)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %w", att.Path, err)
		}
		name := att.FileName
		if name == "" {
			name = filepath.Base(att.Path)
		}
		// The MIME type is derived from the path, never taken from
		// caller hints: image classification decides which ceiling
		// applies, so it must come from the file itself.
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(att.Path)))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		elemType := "file"
		if strings.HasPrefix(mimeType, "image/") {
			elemType = "image"
		}
		out = append(out, protocol.ChatAttachment{
			Type:      elemType,
			MimeType:  mimeType,
			FileName:  name,
			SizeBytes: fi.Size(),
		})
	}
	// 4-step pre-send check against the (possibly defaulted) policy
	// before a single byte is read off disk.
	if err := protocol.ValidateAttachments(out, policy); err != nil {
		return nil, err
	}
	for i := range out {
		data, err := os.ReadFile(atts[i].Path)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %w", atts[i].Path, err)
		}
		out[i].Content = base64.StdEncoding.EncodeToString(data)
	}
	return out, nil
}

// takePendingCatalog returns the System:-prefixed catalog block to
// prepend on the first turn of a session, or "" if the catalog has
// already been delivered (the gateway retains it server-side after
// parsing). The check-and-mark is atomic so concurrent ChatSend
// calls on the same session don't both emit the catalog.
func (b *Backend) takePendingCatalog(sessionKey string, skills []backend.SkillCatalogEntry) string {
	if len(skills) == 0 {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.catalogSent[sessionKey] {
		return ""
	}
	var entries strings.Builder
	for _, s := range skills {
		if s.Name == "" {
			continue
		}
		entries.WriteString(fmt.Sprintf("  - %s: %s\n", s.Name, s.Description))
	}
	if entries.Len() == 0 {
		return ""
	}
	body := "Available agent skills (activate with /skill-name):\n" + entries.String()
	b.catalogSent[sessionKey] = true
	return prefixAllLines(body)
}

// prefixAllLines prepends "System: " to every line of the text so
// the gateway's prompt assembler can identify the block, and so
// stripSystemLines on the client side hides it from the visible
// transcript on history refresh.
func prefixAllLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "System: " + line
	}
	return strings.Join(lines, "\n")
}

func (b *Backend) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	return b.client.ChatAbort(ctx, sessionKey, runID)
}

func (b *Backend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return b.client.ChatHistory(ctx, sessionKey, limit)
}

func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return b.client.ModelsList(ctx)
}

func (b *Backend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return b.client.SessionPatchModel(ctx, sessionKey, modelID)
}

// Capabilities reports the full OpenClaw capability surface — every
// optional sub-interface is implemented below.
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		GatewayStatus:   true,
		RemoteExec:      true,
		SessionCompact:  true,
		Thinking:        true,
		SessionUsage:    true,
		AuthRecovery:    backend.AuthRecoveryDeviceToken,
		AgentWorkspace:  true,
		AgentManagement: true,
		Cron:            true,
	}
}

// --- StatusBackend ---

// Status returns the gateway-side connection status. agentID and
// sessionKey are ignored — OpenClaw's per-session state lives on the
// gateway and is already reflected in the health snapshot.
func (b *Backend) Status(ctx context.Context, agentID, sessionKey string) (*backend.BackendStatus, error) {
	// Fetch the gateway health snapshot. A health-fetch failure must
	// not lose the rest of the status — render with Health=nil so the
	// user still sees endpoint / version / auth details.
	health, healthErr := b.client.GatewayHealth(ctx)

	auth := "anonymous"
	if b.client.HasDeviceToken() {
		auth = "device token"
	}

	status := &backend.BackendStatus{
		Type:     "openclaw",
		Endpoint: b.client.GatewayURL(),
		Auth:     auth,
		Gateway: &backend.GatewayStatus{
			Health:        health,
			UptimeMs:      b.client.HelloUptimeMs(),
			Version:       b.client.HelloServerVersion(),
			APIVersion:    b.client.HelloProtocol(),
			APIVersionMin: protocol.MinProtocolVersion,
			APIVersionMax: protocol.ProtocolVersion,
		},
	}
	return status, healthErr
}

// --- ExecBackend ---

func (b *Backend) ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error) {
	return b.client.ExecRequest(ctx, command, sessionKey)
}

func (b *Backend) ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error) {
	return b.client.ExecResolve(ctx, id, decision)
}

// --- CompactBackend ---

func (b *Backend) SessionCompact(ctx context.Context, sessionKey string) error {
	return b.client.SessionCompact(ctx, sessionKey)
}

// --- ThinkingBackend ---

func (b *Backend) SessionPatchThinking(ctx context.Context, sessionKey, level string) error {
	return b.client.SessionPatchThinking(ctx, sessionKey, level)
}

// --- UsageBackend ---

func (b *Backend) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	return b.client.SessionUsage(ctx, sessionKey)
}

// --- DeviceTokenAuth ---

func (b *Backend) StoreToken(token string) error { return b.client.StoreToken(token) }
func (b *Backend) ClearToken() error             { return b.client.ClearToken() }
func (b *Backend) ResetIdentity() error          { return b.client.ResetIdentity() }

// --- CronBackend ---

func (b *Backend) CronsList(ctx context.Context, params protocol.CronListParams) (*protocol.CronListResult, error) {
	return b.client.CronsList(ctx, params)
}

func (b *Backend) CronRuns(ctx context.Context, params protocol.CronRunsParams) (*protocol.CronRunsResult, error) {
	return b.client.CronRuns(ctx, params)
}

func (b *Backend) CronAdd(ctx context.Context, params protocol.CronAddParams) (json.RawMessage, error) {
	return b.client.CronAdd(ctx, params)
}

func (b *Backend) CronUpdate(ctx context.Context, params protocol.CronUpdateParams) error {
	return b.client.CronUpdate(ctx, params)
}

func (b *Backend) CronUpdateRaw(ctx context.Context, jobID string, patch map[string]any) error {
	return b.client.CronUpdateRaw(ctx, jobID, patch)
}

func (b *Backend) CronRemove(ctx context.Context, jobID string) error {
	return b.client.CronRemove(ctx, jobID)
}

func (b *Backend) CronRun(ctx context.Context, jobID string, force bool) error {
	return b.client.CronRun(ctx, jobID, force)
}

// Compile-time assertions that the wrapper implements every interface
// it claims to.
var (
	_ backend.Backend         = (*Backend)(nil)
	_ backend.StatusBackend   = (*Backend)(nil)
	_ backend.ExecBackend     = (*Backend)(nil)
	_ backend.CompactBackend  = (*Backend)(nil)
	_ backend.ThinkingBackend = (*Backend)(nil)
	_ backend.UsageBackend    = (*Backend)(nil)
	_ backend.DeviceTokenAuth = (*Backend)(nil)
	_ backend.CronBackend     = (*Backend)(nil)
)
