package audit

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
)

// Con lo exportado, quien lo recibe recalcula el hash de cada fila con HashFormula (§11.1).
func TestExportedRowsRecomputeTheirHash(t *testing.T) {
	_, r := chainOf(t, 3)
	var prev string
	err := r.AuditChain(context.Background(), func(row repo.AuditRow) error {
		var buf bytes.Buffer
		NewJSONL(&buf).Write(Export(repo.AuditEntry{AuditRow: row}, true))
		var e Exported
		if err := json.Unmarshal(buf.Bytes(), &e); err != nil {
			return err
		}
		body, _ := json.Marshal([]string{e.At, e.ActorID, e.WsID, e.Action, e.TargetType, e.TargetID, e.MetaJSON})
		sum := sha256.Sum256(append([]byte(e.PrevHash), body...))
		if hex.EncodeToString(sum[:]) != e.Hash {
			return errors.New("hash does not recompute from the exported row")
		}
		if prev != "" && e.PrevHash != prev {
			return errors.New("prev_hash does not chain")
		}
		if e.Actor["ip"] != "10.0.0.1" {
			return errors.New("withIP lost the IP")
		}
		prev = e.Hash
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// La IP solo con withIP; seudonimizado, solo el seudónimo; en el CSV, un nombre que empieza con
// = no se vuelve fórmula.
func TestExportPersonalData(t *testing.T) {
	e := Export(repo.AuditEntry{AuditRow: repo.AuditRow{ID: 1, ActorID: "u", Snapshot: `{"email":"a@x.cl","name":"=HYPERLINK(1)","ip":"10.0.0.1"}`}}, false)
	if e.Actor["ip"] != "" || e.Actor["email"] != "a@x.cl" {
		t.Errorf("without IP: %+v", e.Actor)
	}
	p := Export(repo.AuditEntry{AuditRow: repo.AuditRow{ID: 2, ActorID: "u", Snapshot: `{"pseudonym":"abc"}`}}, true)
	if len(p.Actor) != 1 || p.Actor["pseudonym"] != "abc" {
		t.Errorf("pseudonymized: %+v", p.Actor)
	}
	var buf bytes.Buffer
	w := NewCSV(&buf)
	w.Write(e)
	w.Close()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "id,at,action") || !strings.Contains(lines[1], "'=HYPERLINK(1)") {
		t.Errorf("csv:\n%s", buf.String())
	}
}

type fakeSink struct {
	got  []Exported
	fail bool
}

func (s *fakeSink) Send(_ context.Context, es []Exported) error {
	if s.fail {
		return errors.New("syslog down")
	}
	s.got = append(s.got, es...)
	return nil
}

// La copia retoma donde quedó: con el destino caído no avanza, y después manda lo pendiente
// sin repetir (§11.2).
func TestForwarderResumes(t *testing.T) {
	_, r := chainOf(t, 3)
	ctx := context.Background()
	sink := &fakeSink{}
	fw := Forwarder{Store: r, Sink: sink, Log: slog.New(slog.DiscardHandler)}
	if err := fw.Once(ctx); err != nil || len(sink.got) != 3 {
		t.Fatalf("first pass: %d events, %v", len(sink.got), err)
	}
	rec := NewRecorder(r, time.Now, slog.New(slog.DiscardHandler))
	rec.Record(ctx, Event{Action: BackupRun, System: "daily_backup"})
	sink.fail = true
	if err := fw.Once(ctx); err == nil {
		t.Fatal("a failing sink must return the error")
	}
	if st, _ := r.JobState(ctx, ForwardJob); !strings.Contains(st, `"last_id":3`) || !strings.Contains(st, "syslog down") {
		t.Errorf("state after a failure: %s", st)
	}
	sink.fail = false
	if err := fw.Once(ctx); err != nil || len(sink.got) != 4 || sink.got[3].Action != "backup.run" {
		t.Fatalf("after recovering: %d events, %v", len(sink.got), err)
	}
}

// Syslog RFC 5424 con el largo delante por TCP (RFC 6587); en claro solo hacia la misma máquina.
func TestSyslogSink(t *testing.T) {
	for _, bad := range []string{"tcp://syslog.empresa.cl:514", "udp://10.0.0.5:514", "http://x:1", "tls://sinpuerto"} {
		if _, err := NewSyslogSink(bad, ""); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	if _, err := NewSyslogSink("tls://syslog.empresa.cl:6514", ""); err != nil {
		t.Error(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		n, _ := br.ReadString(' ')
		size, _ := strconv.Atoi(strings.TrimSpace(n))
		msg := make([]byte, size)
		io.ReadFull(br, msg)
		got <- string(msg)
	}()
	s, err := NewSyslogSink("tcp://"+ln.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), []Exported{{ID: 7, At: "2026-11-02T10:00:00.000000Z", Action: "auth.login", MetaJSON: "{}"}}); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	if !strings.HasPrefix(msg, "<85>1 2026-11-02T10:00:00.000000Z ") || !strings.Contains(msg, " kudawiki - audit - {") || !strings.Contains(msg, `"action":"auth.login"`) {
		t.Errorf("syslog message: %q", msg)
	}
}

// El paquete de evidencia (§21.2.7): la firma del manifiesto se comprueba con la llave pública
// que trae, el manifiesto tiene el SHA-256 de cada archivo, solo van los eventos del período, y
// una cadena rota se informa en el paquete.
func TestEvidencePackage(t *testing.T) {
	db, r := chainOf(t, 4)
	// Los dos primeros, en enero; se vuelve a sellar la cadena, que cambiar la fecha rompe.
	exec(t, db, `DROP TRIGGER audit_no_update`, `UPDATE audit_log SET at = '2026-01-15T10:00:00.000000Z' WHERE id IN (1, 2)`)
	var rows []repo.AuditRow
	r.AuditChain(context.Background(), func(row repo.AuditRow) error { rows = append(rows, row); return nil })
	prev := Genesis
	for _, row := range rows {
		hash := Seal(prev, row)
		exec(t, db, fmt.Sprintf(`UPDATE audit_log SET prev_hash = '%s', hash = '%s' WHERE id = %d`, prev, hash, row.ID))
		prev = hash
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	if _, err := WriteEvidence(context.Background(), r, io.Discard, "2026-02-01", "2026-01-01", key, time.Now()); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("reversed range: %v", err)
	}
	var buf bytes.Buffer
	rep, err := WriteEvidence(context.Background(), r, &buf, "2026-01-01", "2026-01-31", key, time.Now())
	if err != nil || rep.Events != 2 || !rep.ChainOK {
		t.Fatalf("report %+v, %v", rep, err)
	}
	files := unzip(t, buf.Bytes())
	block, _ := pem.Decode(files["signing-key.pem"])
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil || !ed25519.Verify(pub.(ed25519.PublicKey), files["manifest.json"], files["manifest.sig"]) {
		t.Fatal("the manifest signature does not verify with the included key")
	}
	var manifest struct{ SHA256 map[string]string }
	json.Unmarshal(files["manifest.json"], &manifest)
	if len(manifest.SHA256) != 4 {
		t.Errorf("manifest lists %d files", len(manifest.SHA256))
	}
	for name, want := range manifest.SHA256 {
		sum := sha256.Sum256(files[name])
		if hex.EncodeToString(sum[:]) != want {
			t.Errorf("%s does not match the manifest", name)
		}
	}
	if n := strings.Count(string(files["audit.jsonl"]), "\n"); n != 2 {
		t.Errorf("audit.jsonl has %d events, want the 2 of January", n)
	}

	exec(t, db, `UPDATE audit_log SET meta_json = '{"revision":99}' WHERE id = 3`)
	buf.Reset()
	if rep, err := WriteEvidence(context.Background(), r, &buf, "2026-01-01", "2026-12-31", key, time.Now()); err != nil || rep.ChainOK || !strings.Contains(rep.ChainError, "event 3") {
		t.Errorf("broken chain: %+v %v", rep, err)
	} else if !bytes.Contains(unzip(t, buf.Bytes())["verify.json"], []byte(`"chain_ok": false`)) {
		t.Error("verify.json does not say the chain is broken")
	}
}

func unzip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		out[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	return out
}
