// Package kudawiki embebe el frontend compilado para que todo viaje en un solo binario (ADR-0001).
package kudawiki

import (
	"embed"
	"io/fs"
)

//go:embed all:ui/dist
var uiDist embed.FS

// Los textos del correo están en los mismos archivos que los de la interfaz (SPEC §12.1).
//
//go:embed ui/src/lib/i18n/es.json ui/src/lib/i18n/en.json
var i18nFiles embed.FS

// UI es el contenido de ui/dist; exige `pnpm build` en ui/ antes de compilar.
func UI() fs.FS {
	sub, err := fs.Sub(uiDist, "ui/dist")
	if err != nil {
		panic(err) // la ruta es constante: si falla, el binario está mal armado
	}
	return sub
}

// I18n son es.json y en.json, de donde el correo toma sus textos.
func I18n() fs.FS {
	sub, err := fs.Sub(i18nFiles, "ui/src/lib/i18n")
	if err != nil {
		panic(err)
	}
	return sub
}
