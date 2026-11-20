package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// QA-02: con los plazos del servidor más cortos que la transferencia, una descarga o una subida
// que avanza de a tramos termina entera si va por streaming/streamingBody; sin ellos se corta
// (el control prueba que la prueba mide algo).
func TestStreamingDeadlines(t *testing.T) {
	const timeout, step, chunks = 300 * time.Millisecond, 100 * time.Millisecond, 6
	serve := func(h http.HandlerFunc) *httptest.Server {
		srv := httptest.NewUnstartedServer(h)
		srv.Config.ReadTimeout, srv.Config.WriteTimeout = timeout, timeout
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	chunk := strings.Repeat("x", 1024)

	download := func(wrap bool) (int, error) {
		srv := serve(func(w http.ResponseWriter, _ *http.Request) {
			out := w
			if wrap {
				out = streaming(w)
			}
			for range chunks {
				if _, err := io.WriteString(out, chunk); err != nil {
					return
				}
				http.NewResponseController(w).Flush()
				time.Sleep(step)
			}
		})
		resp, err := http.Get(srv.URL)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return len(b), err
	}
	if n, err := download(true); err != nil || n != chunks*len(chunk) {
		t.Errorf("slow download with streaming: %d bytes, %v; want %d", n, err, chunks*len(chunk))
	}
	if n, _ := download(false); n == chunks*len(chunk) {
		t.Error("control: without streaming the download should have been cut")
	}

	upload := func(wrap bool) (int, error) {
		got := make(chan int, 1)
		srv := serve(func(w http.ResponseWriter, r *http.Request) {
			if wrap {
				streamingBody(w, r)
			}
			b, _ := io.ReadAll(r.Body)
			got <- len(b)
		})
		pr, pw := io.Pipe()
		go func() {
			for range chunks {
				if _, err := io.WriteString(pw, chunk); err != nil {
					return
				}
				time.Sleep(step)
			}
			pw.Close()
		}()
		resp, err := http.Post(srv.URL, "application/octet-stream", pr)
		if err == nil {
			resp.Body.Close()
		}
		select {
		case n := <-got:
			return n, nil
		case <-time.After(5 * time.Second):
			return 0, err
		}
	}
	if n, err := upload(true); err != nil || n != chunks*len(chunk) {
		t.Errorf("slow upload with streamingBody: %d bytes, %v; want %d", n, err, chunks*len(chunk))
	}
	if n, _ := upload(false); n == chunks*len(chunk) {
		t.Error("control: without streamingBody the upload should have been cut")
	}
}
