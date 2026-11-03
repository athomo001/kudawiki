// Package passkeytest es un autenticador WebAuthn de software para las pruebas de las passkeys
// (SPEC §8.1): crea una credencial ES256 con atestación "none" y firma los desafíos como lo haría
// un navegador. Solo lo importan los tests: no llega al binario.
package passkeytest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/url"
)

// Authenticator guarda una sola credencial. Count es su contador de firmas: bajarlo simula una
// copia de la passkey. SkipUV firma sin verificar a la persona (sin huella ni PIN).
type Authenticator struct {
	Origin         string
	Count          uint32
	SkipUV         bool
	BackupEligible bool

	key        *ecdsa.PrivateKey
	id         []byte
	userHandle []byte
}

func New(origin string) *Authenticator {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 16)
	rand.Read(id)
	return &Authenticator{Origin: origin, key: key, id: id}
}

// ID es el credential ID en base64url, como lo guarda el servidor.
func (a *Authenticator) ID() string { return b64(a.id) }

var b64 = base64.RawURLEncoding.EncodeToString

// Create responde las opciones de navigator.credentials.create() con lo que daría
// PublicKeyCredential.toJSON().
func (a *Authenticator) Create(options []byte) []byte {
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		panic(err)
	}
	a.userHandle, _ = base64.RawURLEncoding.DecodeString(o.PublicKey.User.ID)
	raw, err := a.key.PublicKey.Bytes() // 0x04 | x | y
	if err != nil {
		panic(err)
	}
	x, y := raw[1:33], raw[33:]
	// Clave COSE EC2: {1: 2, 3: -7, -1: 1, -2: x, -3: y}.
	cose := append([]byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}, x...)
	cose = append(append(cose, 0x22, 0x58, 0x20), y...)
	auth := a.authData(0x40) // AT: lleva la credencial
	auth = append(auth, make([]byte, 16)...)
	// #nosec G115 -- solo pruebas; ids y datos de unos pocos bytes
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(a.id)))
	auth = append(append(auth, a.id...), cose...)
	// {"fmt": "none", "attStmt": {}, "authData": auth}
	att := []byte{0xa3, 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e', 0x67, 'a', 't', 't', 'S', 't', 'm', 't', 0xa0,
		0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a', 0x59}
	// #nosec G115 -- solo pruebas; ids y datos de unos pocos bytes
	att = append(binary.BigEndian.AppendUint16(att, uint16(len(auth))), auth...)
	return a.response(map[string]any{
		"clientDataJSON":    b64(a.clientData("webauthn.create", o.PublicKey.Challenge)),
		"attestationObject": b64(att),
		"transports":        []string{"internal"},
	})
}

// Get firma el desafío de navigator.credentials.get().
func (a *Authenticator) Get(options []byte) []byte {
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		panic(err)
	}
	a.Count++
	auth := a.authData(0)
	client := a.clientData("webauthn.get", o.PublicKey.Challenge)
	sum := sha256.Sum256(client)
	digest := sha256.Sum256(append(bytes.Clone(auth), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		panic(err)
	}
	return a.response(map[string]any{
		"clientDataJSON":    b64(client),
		"authenticatorData": b64(auth),
		"signature":         b64(sig),
		"userHandle":        b64(a.userHandle),
	})
}

func (a *Authenticator) authData(extra byte) []byte {
	u, _ := url.Parse(a.Origin)
	rp := sha256.Sum256([]byte(u.Hostname()))
	flags := byte(0x01) | extra // UP
	if !a.SkipUV {
		flags |= 0x04
	}
	if a.BackupEligible {
		flags |= 0x08 | 0x10 // BE y BS: sincronizada, como las de iCloud o Google
	}
	return binary.BigEndian.AppendUint32(append(rp[:], flags), a.Count)
}

func (a *Authenticator) clientData(kind, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": a.Origin, "crossOrigin": false})
	return b
}

func (a *Authenticator) response(r map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"id": b64(a.id), "rawId": b64(a.id), "type": "public-key",
		"clientExtensionResults": map[string]any{}, "authenticatorAttachment": "platform", "response": r})
	return b
}
