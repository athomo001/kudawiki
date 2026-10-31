package mail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.DiscardHandler)

type outboxRow struct {
	attempts        int
	pending         bool
	sent, failed    bool
	next, lastError string
}

func (f fixture) outbox(t *testing.T) []outboxRow {
	t.Helper()
	rows, err := f.db.Read.Query(`SELECT attempts, payload_enc IS NOT NULL, sent_at IS NOT NULL, failed_at IS NOT NULL, next_attempt_at, coalesce(last_error, '') FROM email_outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		rows.Scan(&r.attempts, &r.pending, &r.sent, &r.failed, &r.next, &r.lastError)
		out = append(out, r)
	}
	return out
}

func TestQueueDelivers(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	srv, _ := startFakeSMTP(t, false)
	host, port := hostPort(t, srv.addr)
	f.svc.Update(ctx, f.super.ID, Patch{Host: new(host), Port: new(port), Security: new("none"), From: new("wiki@acme.cl")})

	if err := f.svc.Enqueue(ctx, Message{To: "ana@acme.cl", Template: "no-existe"}); !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("unknown template: %v", err)
	}
	token := "tok-secreto-de-invitacion"
	if err := f.svc.Enqueue(ctx, Message{To: "ana@acme.cl", Lang: "es", Template: "invite",
		Vars: map[string]string{"name": "Ana", "inviter": "Super", "days": "7", "email": "ana@acme.cl"}, URL: "https://wiki.acme.cl/invite#" + token}); err != nil {
		t.Fatal(err)
	}
	// El cuerpo no está en claro: ni el token ni el destinatario.
	var enc []byte
	f.db.Read.QueryRow(`SELECT payload_enc FROM email_outbox`).Scan(&enc)
	if bytes.Contains(enc, []byte(token)) || bytes.Contains(enc, []byte("ana@acme.cl")) {
		t.Error("the queued email is readable in the database")
	}
	select {
	case <-f.svc.wake:
	default:
		t.Error("Enqueue did not wake the worker")
	}

	if err := f.svc.Flush(ctx, quiet); err != nil {
		t.Fatal(err)
	}
	got := srv.messages()
	if len(got) != 1 || got[0].to != "ana@acme.cl" {
		t.Fatalf("received = %+v", got)
	}
	_, body, _ := parts(t, got[0].data)
	if text := string(body["text/plain"]); !strings.Contains(text, "Super te dio una cuenta") || !strings.Contains(text, "/invite#"+token) {
		t.Errorf("invite text:\n%s", text)
	}
	if rows := f.outbox(t); len(rows) != 1 || rows[0].pending || !rows[0].sent || rows[0].attempts != 1 {
		t.Errorf("after sending: %+v", rows)
	}
	// Una segunda pasada no lo manda otra vez.
	f.svc.Flush(ctx, quiet)
	if len(srv.messages()) != 1 {
		t.Error("a sent email went out twice")
	}
}

// Un servidor que rechaza: reintentos con espera creciente y, al décimo, la cola se rinde.
func TestQueueRetriesAndGivesUp(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	srv, _ := startFakeSMTP(t, false)
	srv.rejectTo = "ana@acme.cl"
	host, port := hostPort(t, srv.addr)
	f.svc.Update(ctx, f.super.ID, Patch{Host: new(host), Port: new(port), Security: new("none"), From: new("wiki@acme.cl")})
	f.svc.Enqueue(ctx, Message{To: "ana@acme.cl", Lang: "es", Template: "password_changed", Vars: map[string]string{"name": "Ana", "when": "hoy"}})

	f.svc.Flush(ctx, quiet)
	r := f.outbox(t)[0]
	if r.attempts != 1 || !r.pending || !strings.Contains(r.lastError, "550") || !strings.HasPrefix(r.next, "2026-10-30T12:01:00") {
		t.Fatalf("first failure: %+v", r)
	}
	// Antes de su turno no se intenta.
	f.svc.Flush(ctx, quiet)
	if f.outbox(t)[0].attempts != 1 {
		t.Error("retried before its turn")
	}
	waits := []time.Duration{}
	for i := 1; i < maxAttempts; i++ {
		before := f.outbox(t)[0].next
		next, _ := time.Parse(time.RFC3339, before)
		waits = append(waits, next.Sub(now))
		now = next
		f.svc.Flush(ctx, quiet)
	}
	if waits[1] != 2*time.Minute || waits[2] != 4*time.Minute || waits[8] != time.Hour {
		t.Errorf("waits = %v", waits)
	}
	r = f.outbox(t)[0]
	if r.attempts != maxAttempts || r.pending || !r.failed || r.sent {
		t.Errorf("after giving up: %+v", r)
	}
}

// Sin correo configurado, lo encolado espera y se reintenta: no se pierde.
func TestQueueWithoutSMTP(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	if ok, _ := f.svc.Enabled(ctx); ok {
		t.Fatal("enabled without SMTP")
	}
	f.svc.Enqueue(ctx, Message{To: "ana@acme.cl", Lang: "es", Template: "password_changed", Vars: map[string]string{"name": "Ana"}})
	f.svc.Flush(ctx, quiet)
	if r := f.outbox(t)[0]; r.attempts != 1 || !r.pending || !strings.Contains(r.lastError, "smtp_not_configured") {
		t.Errorf("without SMTP: %+v", r)
	}
}

// TestNotificationMail: los avisos llevan la baja en un clic (RFC 8058) en los encabezados y en
// el pie, y el resumen diario, la lista de avisos en el HTML y en el texto (§12.1).
func TestNotificationMail(t *testing.T) {
	f := newFixture(t, Patch{})
	ctx := context.Background()
	srv, _ := startFakeSMTP(t, false)
	host, port := hostPort(t, srv.addr)
	f.svc.Update(ctx, f.super.ID, Patch{Host: new(host), Port: new(port), Security: new("none"), From: new("wiki@acme.cl")})
	unsub := "https://wiki.acme.cl/unsubscribe?t=u.all.firma"
	if err := f.svc.Enqueue(ctx, Message{To: "ana@acme.cl", Lang: "es", Template: "digest", URL: "https://wiki.acme.cl/",
		Vars: map[string]string{"name": "Ana", "count": "2"}, Unsubscribe: unsub, Lines: []Line{
			{Template: "page_changed", Vars: map[string]string{"title": "VPN", "actor": "Ben", "area": "TI"}},
			{Template: "proposal_pending", Vars: map[string]string{"title": "Correo", "actor": "Ben", "area": "TI"}},
		}}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Enqueue(ctx, Message{To: "ana@acme.cl", Template: "digest", Lines: []Line{{Template: "no-existe"}}}); !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("unknown line template: %v", err)
	}
	if err := f.svc.Flush(ctx, quiet); err != nil {
		t.Fatal(err)
	}
	got := srv.messages()
	if len(got) != 1 {
		t.Fatalf("received = %+v", got)
	}
	msg, body, _ := parts(t, got[0].data)
	if msg.Header.Get("List-Unsubscribe") != "<"+unsub+">" || msg.Header.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Errorf("unsubscribe headers: %v", msg.Header)
	}
	html, text := string(body["text/html"]), string(body["text/plain"])
	for _, want := range []string{"VPN", "Correo", unsub} {
		if !strings.Contains(html, want) || !strings.Contains(text, want) {
			t.Errorf("digest is missing %q:\n%s\n%s", want, html, text)
		}
	}
}
