package branding

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"slices"
	"testing"
)

// El logo de Kudawiki (spec/logo/kudawiki-logo.svg), que oksvg sabe dibujar.
const capasSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">
<rect x="15" y="3" width="30" height="30" rx="4" fill="#BCD6FB"/>
<rect x="9" y="9" width="30" height="30" rx="4" fill="#3B82F6"/>
<rect x="3" y="15" width="30" height="30" rx="4" fill="#1D4F91"/>
<path d="M13.5 22v16M13.5 31l8-9M16 28.5l6.5 9.5" fill="none" stroke="#fff" stroke-width="3.2" stroke-linecap="round"/>
</svg>`

func pngOf(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func variants(p processed) []string {
	var out []string
	for _, a := range p.assets {
		out = append(out, a.variant)
	}
	return out
}

func codes(ws []Warning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}

func TestProcessSVGLogo(t *testing.T) {
	p, err := process("logo", []byte(capasSVG), DefaultHeader, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := variants(p); !slices.Equal(got, []string{"original", "ico", "png_32", "png_180", "png_512"}) {
		t.Fatalf("variants = %v", got)
	}
	if len(p.warnings) != 0 {
		t.Errorf("warnings = %v", codes(p.warnings))
	}
	if p.assets[0].mime != "image/svg+xml" {
		t.Errorf("original mime = %s", p.assets[0].mime)
	}
	// El .ico trae 16 y 32; el de Apple tiene fondo blanco (sin transparencia en la esquina).
	ico := p.assets[1].data
	if binary.LittleEndian.Uint16(ico[2:]) != 1 || binary.LittleEndian.Uint16(ico[4:]) != 2 || ico[6] != 16 || ico[22] != 32 {
		t.Errorf("ico header = % x", ico[:38])
	}
	apple, err := png.Decode(bytes.NewReader(p.assets[3].data))
	if err != nil {
		t.Fatal(err)
	}
	if b := apple.Bounds(); b.Dx() != 180 || b.Dy() != 180 {
		t.Errorf("apple icon = %v", b)
	}
	if _, _, _, a := apple.At(0, 0).RGBA(); a != 0xffff {
		t.Errorf("apple icon corner is transparent")
	}
	// El logo se dibuja de verdad: el centro del de 512 no está vacío.
	big, _ := png.Decode(bytes.NewReader(p.assets[4].data))
	if _, _, _, a := big.At(256, 256).RGBA(); a == 0 {
		t.Error("512 icon is empty")
	}
}

func TestProcessWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		data       []byte
		header     string
		onBar      bool
		want       []string
	}{
		{"logo chico", "logo", pngOf(t, 120, 40, color.White), DefaultHeader, false, []string{"logo_too_small"}},
		{"favicon rectangular", "favicon", pngOf(t, 64, 32, color.Black), DefaultHeader, false, []string{"favicon_not_square"}},
		{"logo oscuro sobre barra oscura", "logo_dark", pngOf(t, 300, 80, color.RGBA{0x20, 0x20, 0x20, 0xff}), DefaultHeader, true, []string{"logo_low_contrast"}},
		{"logo claro sobre barra oscura", "logo_dark", pngOf(t, 300, 80, color.White), DefaultHeader, true, nil},
		{"svg con texto", "favicon", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><text>K</text></svg>`), DefaultHeader, false, []string{"svg_not_rasterized"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := process(tc.kind, tc.data, tc.header, tc.onBar)
			if err != nil {
				t.Fatal(err)
			}
			if got := codes(p.warnings); !slices.Equal(got, tc.want) {
				t.Errorf("warnings = %v, want %v", got, tc.want)
			}
		})
	}
	// Un SVG que no se rasteriza se guarda igual, solo con su original.
	p, _ := process("favicon", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><text>K</text></svg>`), DefaultHeader, false)
	if got := variants(p); !slices.Equal(got, []string{"original"}) {
		t.Errorf("variants = %v", got)
	}
	// El logo oscuro no lleva íconos: solo va en la barra.
	p, _ = process("logo_dark", []byte(capasSVG), DefaultHeader, false)
	if got := variants(p); !slices.Equal(got, []string{"original"}) {
		t.Errorf("logo_dark variants = %v", got)
	}
}

func TestProcessRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		want error
	}{
		"grande":    {append([]byte("<svg>"), make([]byte, MaxAssetBytes)...), ErrTooLarge},
		"html":      {[]byte(`<html><script>alert(1)</script></html>`), ErrUnsupportedType},
		"jpeg":      {[]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), ErrUnsupportedType},
		"png falso": {[]byte("\x89PNG\r\n\x1a\nnada"), ErrUnsupportedType},
	} {
		if _, err := process("logo", tc.data, DefaultHeader, false); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}
