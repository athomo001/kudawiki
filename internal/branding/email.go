package branding

import (
	"bytes"
	"context"
	"image/png"
	"sync"

	"github.com/athomo001/kudawiki/internal/repo"
)

// El logo del correo (§12) se ve a 200 × 40 px como máximo; se genera al doble para pantallas 2x.
const emailLogoW, emailLogoH = 400, 80

// emailLogo guarda el último PNG generado: el original cambia poco y cada correo lo necesita.
type emailLogo struct {
	mu     sync.Mutex
	sha256 string
	png    []byte
}

// EmailLogo es el logo de la empresa para incrustar en el correo: PNG, porque Gmail y Outlook no
// muestran SVG. Sale del original guardado cada vez que este cambia, sin una variante más en la
// base. repo.ErrNotFound si la empresa no subió logo o es un SVG que no se puede rasterizar: el
// correo pone el título del sitio en su lugar.
func (s *Service) EmailLogo(ctx context.Context) ([]byte, error) {
	orig, err := s.store.SiteAsset(ctx, "logo", "original")
	if err != nil {
		return nil, err
	}
	c := &s.email
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sha256 == orig.SHA256 {
		return c.png, nil
	}
	src, err := decodeSource(orig.Data)
	if err != nil {
		return nil, err
	}
	img := src.fit(emailLogoW, emailLogoH)
	if img == nil {
		return nil, repo.ErrNotFound
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	c.sha256, c.png = orig.SHA256, b.Bytes()
	return c.png, nil
}

// EmailColors son los colores del correo: el botón con el principal de la empresa y su texto
// por contraste, y los enlaces con el mismo tono que en la wiki (§15.1.2).
type EmailColors struct{ Button, OnButton, Link string }

func (s Site) EmailColors() EmailColors {
	link := s.Palette.Light["--kw-link"]
	if link == "" {
		link = lightDefaults["--kw-link"]
	}
	return EmailColors{Button: s.ColorPrimary, OnButton: textOn(s.ColorPrimary), Link: link}
}
