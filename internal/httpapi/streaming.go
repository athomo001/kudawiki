package httpapi

import (
	"io"
	"net/http"
	"time"
)

// transferIdle es cuánto puede quedar quieta una transferencia larga antes de cortarla. Los plazos
// del servidor (ReadTimeout 30 s, WriteTimeout 60 s, cmd/kudawiki) valen para el pedido entero:
// cortaban un respaldo o un ZIP que tardaba más en bajar, y un ZIP de importación o un archivo que
// tardaba más en subir (QA-02). Aquí el plazo se corre con cada tramo que pasa: una conexión
// lenta que avanza sigue; una que se quedó colgada se corta igual.
const transferIdle = time.Minute

// streamingWriter corre el plazo de escritura con cada tramo de la respuesta.
type streamingWriter struct {
	http.ResponseWriter
	rc *http.ResponseController
}

func (s streamingWriter) Write(b []byte) (int, error) {
	_ = s.rc.SetWriteDeadline(time.Now().Add(transferIdle)) // sin soporte, rige el plazo de siempre
	return s.ResponseWriter.Write(b)
}

func (s streamingWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// streaming envuelve la respuesta de una descarga que puede tardar más que WriteTimeout.
func streaming(w http.ResponseWriter) http.ResponseWriter {
	return streamingWriter{ResponseWriter: w, rc: http.NewResponseController(w)}
}

// streamingReader corre el plazo de lectura con cada tramo del cuerpo.
type streamingReader struct {
	io.ReadCloser
	rc *http.ResponseController
}

func (s streamingReader) Read(b []byte) (int, error) {
	_ = s.rc.SetReadDeadline(time.Now().Add(transferIdle))
	return s.ReadCloser.Read(b)
}

// streamingBody prepara una subida que puede tardar más que ReadTimeout. Va antes de leer el
// cuerpo (MultipartReader incluido).
func streamingBody(w http.ResponseWriter, r *http.Request) {
	r.Body = streamingReader{ReadCloser: r.Body, rc: http.NewResponseController(w)}
}
