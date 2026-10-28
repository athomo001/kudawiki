// Package kudawiki embebe el frontend compilado para que todo viaje en un solo binario (ADR-0001).
package kudawiki

import (
	"embed"
	"io/fs"
)

//go:embed all:ui/dist
var uiDist embed.FS

// UI es el contenido de ui/dist; exige `pnpm build` en ui/ antes de compilar.
func UI() fs.FS {
	sub, err := fs.Sub(uiDist, "ui/dist")
	if err != nil {
		panic(err) // la ruta es constante: si falla, el binario está mal armado
	}
	return sub
}
