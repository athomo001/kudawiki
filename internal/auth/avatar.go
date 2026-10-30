package auth

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // decodificadores de las fotos que se aceptan
	"image/png"
	"slices"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Foto de perfil (SPEC §8.6.3): PNG cuadrado de 128 px. La misma conversión servirá para la
// foto que traiga el directorio (thumbnailPhoto, F6).
const (
	MaxAvatarBytes  = 5 << 20 // una foto de celular entra; igual se re-codifica a 128 px
	avatarSize      = 128
	maxAvatarPixels = 40_000_000 // más que esto no se decodifica: evita bombas de descompresión
)

var (
	ErrAvatarTooLarge = errors.New("file_too_large")
	ErrAvatarType     = errors.New("unsupported_file_type")
)

// avatarPNG recorta al centro el cuadrado más grande y lo lleva a 128 px. Solo PNG, JPEG o WebP:
// un SVG es un documento que podría ejecutar algo.
func avatarPNG(data []byte) ([]byte, error) {
	if len(data) > MaxAvatarBytes {
		return nil, ErrAvatarTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !slices.Contains([]string{"png", "jpeg", "webp"}, format) ||
		cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxAvatarPixels {
		return nil, ErrAvatarType
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrAvatarType
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x, y := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, image.Rect(x, y, x+side, y+side), draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
