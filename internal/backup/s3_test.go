package backup

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// La firma SigV4 coincide con el ejemplo de la documentación de AWS ("GET Object", Signature
// Version 4 examples): mismos datos, misma firma.
func TestSigV4KnownVector(t *testing.T) {
	s := &S3{Endpoint: &url.URL{Scheme: "https", Host: "s3.amazonaws.com"}, Region: "us-east-1", Bucket: "examplebucket",
		AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }}
	req, _ := http.NewRequest("GET", s.objectURL("test.txt"), nil)
	req.Header.Set("Range", "bytes=0-9")
	s.sign(req)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("authorization:\n got %s\nwant %s", got, want)
	}
	if req.URL.Host != "examplebucket.s3.amazonaws.com" {
		t.Errorf("virtual-hosted style host: %s", req.URL.Host)
	}
}

// Sube y baja contra un servidor de prueba con el bucket en la ruta (MinIO); un error de S3
// llega con su código; el endpoint tiene que ser https salvo a la misma máquina.
func TestS3PutGet(t *testing.T) {
	objects := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=key/") {
			http.Error(w, "<Code>AccessDenied</Code>", http.StatusForbidden)
			return
		}
		switch r.Method {
		case "PUT":
			b, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = b
		case "GET":
			b, ok := objects[r.URL.Path]
			if !ok {
				http.Error(w, "<Code>NoSuchKey</Code>", http.StatusNotFound)
				return
			}
			w.Write(b)
		}
	}))
	defer srv.Close()
	s, err := NewS3(srv.URL, "us-east-1", "respaldos", "wiki", "key", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, "a.tar.zst.enc", bytes.NewReader([]byte("hola")), 4); err != nil {
		t.Fatal(err)
	}
	if _, ok := objects["/respaldos/wiki/a.tar.zst.enc"]; !ok {
		t.Fatalf("object stored at %v", objects)
	}
	body, err := s.Get(ctx, "a.tar.zst.enc")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if string(got) != "hola" {
		t.Errorf("got %q", got)
	}
	if _, err := s.Get(ctx, "no-existe"); err == nil || !strings.Contains(err.Error(), "NoSuchKey") {
		t.Errorf("missing object: %v", err)
	}
	if err := s.Put(ctx, "x", bytes.NewReader(nil), MaxPut+1); err != ErrTooLargeForPut {
		t.Errorf("over 5 GiB: %v", err)
	}
	for _, bad := range []string{"http://minio.empresa.cl:9000", "ftp://x", "https://x/ruta"} {
		if _, err := NewS3(bad, "r", "b", "", "k", "s", true); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	if _, err := NewS3("https://s3.amazonaws.com", "", "b", "", "k", "s", false); err == nil {
		t.Error("a missing region must be rejected")
	}
}
