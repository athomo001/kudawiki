package audit

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"time"

	"github.com/athomo001/kudawiki/internal/repo"
)

type evidenceStore interface {
	chain
	AuditRange(ctx context.Context, from, to string, fn func(repo.AuditRow) error) error
}

// EvidenceReport es lo que quedó en el paquete.
type EvidenceReport struct {
	Events     int
	ChainOK    bool
	ChainError string
	// KeyID son los primeros 16 hex del SHA-256 de la llave pública: con él quien recibe el
	// paquete confirma por otro canal que es la llave de esta instalación.
	KeyID string
}

// ErrInvalidRange: las fechas no son AAAA-MM-DD o from es posterior a to.
var ErrInvalidRange = errors.New("from and to must be dates (YYYY-MM-DD) with from <= to")

// WriteEvidence arma el paquete de evidencia de §21.2.7 para entregar a un auditor: un ZIP con
// los eventos del período (audit.jsonl, todo lo de cada fila para recalcular su hash), el
// resultado de verificar la cadena completa (verify.json), un manifiesto con el SHA-256 de cada
// archivo, la firma Ed25519 del manifiesto y la llave pública para comprobarla con OpenSSL
// (README.txt dice cómo). Si la cadena está rota el paquete igual se arma: la rotura es
// evidencia; ChainOK lo dice.
func WriteEvidence(ctx context.Context, s evidenceStore, w io.Writer, from, to string, key ed25519.PrivateKey, now time.Time) (EvidenceReport, error) {
	f, err1 := time.Parse(time.DateOnly, from)
	t, err2 := time.Parse(time.DateOnly, to)
	if err1 != nil || err2 != nil || f.After(t) {
		return EvidenceReport{}, ErrInvalidRange
	}
	var rep EvidenceReport
	var events bytes.Buffer
	enc := NewJSONL(&events)
	var first, last repo.AuditRow
	if err := s.AuditRange(ctx, from, to, func(row repo.AuditRow) error {
		if rep.Events == 0 {
			first = row
		}
		last = row
		rep.Events++
		return enc.Write(Export(repo.AuditEntry{AuditRow: row}, true))
	}); err != nil {
		return rep, err
	}
	chain, err := Verify(ctx, s)
	var broken BrokenError
	switch {
	case errors.As(err, &broken):
		rep.ChainError = broken.Error()
	case err != nil:
		return rep, err
	default:
		rep.ChainOK = true
	}
	verify := map[string]any{
		"chain_ok": rep.ChainOK, "chain_error": rep.ChainError,
		"chain": map[string]any{"events": chain.Events, "first_id": chain.FirstID, "last_id": chain.LastID},
		"range": map[string]any{"from": from, "to": to, "events": rep.Events, "first_id": first.ID, "last_id": last.ID,
			"first_prev_hash": first.PrevHash, "last_hash": last.Hash},
		"hash_formula": HashFormula,
	}
	verifyJSON, _ := json.MarshalIndent(verify, "", "  ")

	pub := key.Public().(ed25519.PublicKey)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	sum := sha256.Sum256(pub)
	rep.KeyID = hex.EncodeToString(sum[:8])

	files := []struct {
		name string
		body []byte
	}{{"audit.jsonl", events.Bytes()}, {"verify.json", verifyJSON}, {"README.txt", []byte(evidenceReadme)}, {"signing-key.pem", pubPEM}}
	hashes := map[string]string{}
	for _, file := range files {
		s := sha256.Sum256(file.body)
		hashes[file.name] = hex.EncodeToString(s[:])
	}
	manifest, _ := json.MarshalIndent(map[string]any{"generated_at": now.UTC().Format(time.RFC3339), "from": from, "to": to,
		"events": rep.Events, "chain_ok": rep.ChainOK, "key_id": rep.KeyID, "sha256": hashes}, "", "  ")
	files = append(files,
		struct {
			name string
			body []byte
		}{"manifest.json", manifest},
		struct {
			name string
			body []byte
		}{"manifest.sig", ed25519.Sign(key, manifest)})

	zw := zip.NewWriter(w)
	for _, file := range files {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: now.UTC()})
		if err != nil {
			return rep, err
		}
		if _, err := fw.Write(file.body); err != nil {
			return rep, err
		}
	}
	return rep, zw.Close()
}

const evidenceReadme = `Kudawiki audit evidence package (ISO/IEC 27001 A.5.18, A.8.15)

audit.jsonl      one event per line, with everything needed to recompute its hash
verify.json      result of checking the whole hash chain when the package was made,
                 and the first prev_hash and last hash of the period
manifest.json    SHA-256 of every file above
manifest.sig     Ed25519 signature of manifest.json
signing-key.pem  the public key; confirm its key_id (manifest.json) with the installation
                 through another channel before trusting it

1. Check the signature:
   openssl pkeyutl -verify -pubin -inkey signing-key.pem -rawin -in manifest.json -sigfile manifest.sig
2. Check each file against manifest.json:
   sha256sum audit.jsonl verify.json README.txt signing-key.pem
3. Each event can be checked on its own:
   ` + HashFormula + `
   and each event's prev_hash is the hash of the event before it.
`
