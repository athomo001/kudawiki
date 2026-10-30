package branding

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decodificador: se aceptan logos WebP (§15.1.3)
)

// Logo y favicon (SPEC §15.1.3): SVG, PNG o WebP de hasta 1 MB.
const (
	MaxAssetBytes = 1 << 20
	maxPixels     = 16_000_000 // más que esto no se decodifica: evita bombas de descompresión
	retinaHeight  = 64         // el logo se ve a 32 px de alto en la barra, 64 en pantallas 2x
)

// Kinds son los tres archivos que sube la empresa.
var Kinds = []string{"logo", "logo_dark", "favicon"}

// asset es una variante lista para guardar.
type asset struct {
	variant, mime string
	data          []byte
}

// processed es lo que sale de una subida: el original (un SVG ya sanitizado) y las variantes
// que pide la fase. Las del correo (F4) y la marca de agua (F7) se generan con su función, desde
// el original guardado.
type processed struct {
	assets   []asset
	warnings []Warning
}

// process valida el archivo y arma sus variantes. header es el color de la barra, para avisar
// de un logo que no se va a ver sobre ella; onBar dice si este archivo es el que va en la barra.
func process(kind string, data []byte, header string, onBar bool) (processed, error) {
	if len(data) > MaxAssetBytes {
		return processed{}, ErrTooLarge
	}
	src, err := decodeSource(data)
	if err != nil {
		return processed{}, err
	}
	out := processed{assets: []asset{{"original", src.mime, src.data}}, warnings: []Warning{}}

	w, h := src.width, src.height
	if kind == "favicon" && math.Abs(w-h) > math.Max(w, h)*0.01 {
		out.warnings = append(out.warnings, Warning{Code: "favicon_not_square"})
	}
	if kind != "favicon" && src.raster != nil && h < retinaHeight {
		out.warnings = append(out.warnings, Warning{Code: "logo_too_small"})
	}
	if kind != "favicon" && onBar {
		if avg, ok := averageColor(src.render(64)); ok && contrast(avg, header) < 3 {
			out.warnings = append(out.warnings, Warning{Code: "logo_low_contrast"})
		}
	}
	// El logo oscuro solo va en la barra; los íconos salen del favicon o, si no hay, del logo.
	if kind == "logo_dark" {
		return out, nil
	}
	icons, err := iconVariants(src)
	if errors.Is(err, errNoRaster) {
		out.warnings = append(out.warnings, Warning{Code: "svg_not_rasterized"})
		return out, nil
	}
	if err != nil {
		return processed{}, err
	}
	out.assets = append(out.assets, icons...)
	return out, nil
}

var errNoRaster = errors.New("svg cannot be rasterized")

// source es la imagen subida, lista para dibujarse a cualquier tamaño.
type source struct {
	mime          string
	data          []byte // lo que se guarda como original
	width, height float64
	raster        image.Image // nil si es SVG
	svg           []byte
}

func decodeSource(data []byte) (source, error) {
	switch http.DetectContentType(data) {
	case "image/png", "image/webp":
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width*cfg.Height > maxPixels {
			return source{}, ErrUnsupportedType
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return source{}, ErrUnsupportedType
		}
		return source{mime: "image/" + format, data: data, width: float64(cfg.Width), height: float64(cfg.Height), raster: img}, nil
	}
	clean, err := sanitizeSVG(data)
	if err != nil {
		return source{}, ErrUnsupportedType
	}
	src := source{mime: "image/svg+xml", data: clean, svg: clean}
	icon, err := readSVG(clean)
	if err != nil || icon.ViewBox.W <= 0 || icon.ViewBox.H <= 0 {
		// Se guarda igual: el navegador lo dibuja; lo que no sale son las variantes PNG.
		src.width, src.height = 1, 1
		return src, nil
	}
	src.width, src.height = icon.ViewBox.W, icon.ViewBox.H
	return src, nil
}

// readSVG lee con el modo estricto: un SVG con algo que oksvg no sabe dibujar (filtros, texto)
// no se rasteriza a medias; se le pide a la empresa un PNG (§15.1.3).
func readSVG(svg []byte) (icon *oksvg.SvgIcon, err error) {
	defer func() { // oksvg no se mantiene desde 2022: un pánico suyo no tumba la subida
		if recover() != nil {
			icon, err = nil, errNoRaster
		}
	}()
	icon, err = oksvg.ReadIconStream(bytes.NewReader(svg), oksvg.StrictErrorMode)
	if err != nil {
		return nil, errNoRaster
	}
	return icon, nil
}

// render dibuja la imagen con su lado mayor de size px; nil si un SVG no se puede rasterizar.
func (s source) render(size int) image.Image { return s.fit(size, size) }

// fit dibuja la imagen lo más grande que entre en maxW × maxH, sin deformarla.
func (s source) fit(maxW, maxH int) image.Image {
	scale := math.Min(float64(maxW)/s.width, float64(maxH)/s.height)
	w, h := max(1, int(math.Round(s.width*scale))), max(1, int(math.Round(s.height*scale)))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if s.raster != nil {
		draw.CatmullRom.Scale(out, out.Bounds(), s.raster, s.raster.Bounds(), draw.Over, nil)
		return out
	}
	if err := drawSVG(s.svg, out); err != nil {
		return nil
	}
	return out
}

func drawSVG(svg []byte, dst *image.RGBA) (err error) {
	icon, err := readSVG(svg)
	if err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = errNoRaster
		}
	}()
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	icon.SetTarget(0, 0, float64(w), float64(h))
	icon.Draw(rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, dst, dst.Bounds())), 1)
	return nil
}

// iconVariants arma los íconos de §15.1.3: favicon.ico (16 y 32), 32, 180 para Apple (con
// fondo blanco) y 512 para el manifiesto.
func iconVariants(src source) ([]asset, error) {
	square := func(size int, bg color.Color, pad float64) (image.Image, error) {
		inner := int(math.Round(float64(size) * (1 - 2*pad)))
		img := src.render(inner)
		if img == nil {
			return nil, errNoRaster
		}
		out := image.NewRGBA(image.Rect(0, 0, size, size))
		if bg != nil {
			draw.Draw(out, out.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
		}
		b := img.Bounds()
		at := image.Pt((size-b.Dx())/2, (size-b.Dy())/2)
		draw.Draw(out, b.Add(at), img, b.Min, draw.Over)
		return out, nil
	}
	var pngs = map[int][]byte{}
	for _, v := range []struct {
		size int
		bg   color.Color
		pad  float64
	}{{16, nil, 0}, {32, nil, 0}, {180, color.White, 0.1}, {512, nil, 0}} {
		img, err := square(v.size, v.bg, v.pad)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return nil, err
		}
		pngs[v.size] = b.Bytes()
	}
	return []asset{
		{"ico", "image/x-icon", encodeICO(pngs[16], pngs[32])},
		{"png_32", "image/png", pngs[32]},
		{"png_180", "image/png", pngs[180]},
		{"png_512", "image/png", pngs[512]},
	}, nil
}

// encodeICO arma un .ico con PNG adentro (válido desde Windows Vista y en todos los navegadores).
// Cada PNG es cuadrado; su lado sale de la cabecera IHDR.
func encodeICO(pngs ...[]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(pngs))})
	offset := 6 + 16*len(pngs)
	for _, p := range pngs {
		side := binary.BigEndian.Uint32(p[16:20])
		binary.Write(&b, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, Bits           uint16
			Size, Offset           uint32
		}{uint8(side), uint8(side), 0, 0, 1, 32, uint32(len(p)), uint32(offset)})
		offset += len(p)
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}

// averageColor es el color promedio de los píxeles visibles, para medir el contraste del logo
// contra la barra.
func averageColor(img image.Image) (string, bool) {
	if img == nil {
		return "", false
	}
	var r, g, bl, n float64
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 128 {
				continue
			}
			r, g, bl, n = r+float64(c.R), g+float64(c.G), bl+float64(c.B), n+1
		}
	}
	if n == 0 {
		return "", false
	}
	return hexOf(rgb{r / n / 255, g / n / 255, bl / n / 255}), true
}
