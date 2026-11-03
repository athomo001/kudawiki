package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 sube y baja respaldos de un bucket S3-compatible (AWS, MinIO, Wasabi y otros; §13.3,
// §21.2.4). Solo PUT y GET de un objeto, con la firma SigV4 hecha aquí: así no entra un SDK
// entero como dependencia para dos llamadas.
type S3 struct {
	Endpoint  *url.URL // https://s3.us-east-1.amazonaws.com o https://minio.empresa.cl:9000
	Region    string
	Bucket    string
	Prefix    string // carpeta dentro del bucket, ej. "kudawiki/"
	AccessKey string
	SecretKey string
	// PathStyle pone el bucket en la ruta (https://host/bucket/objeto), como pide MinIO; si no,
	// va en el nombre (https://bucket.host/objeto), como AWS.
	PathStyle bool
	Client    *http.Client
	Now       func() time.Time
}

// MaxPut es lo más grande que S3 acepta en un solo PUT.
const MaxPut = 5 << 30

// ErrTooLargeForPut: el respaldo pasa de 5 GiB, el máximo de un PUT; hace falta otro destino o
// un respaldo más chico.
var ErrTooLargeForPut = errors.New("the backup is larger than 5 GiB, the most S3 takes in one upload")

// NewS3 valida la configuración. El endpoint tiene que ser https://, salvo hacia la misma
// máquina (§21.2.1: lo que sale del servidor va cifrado).
func NewS3(endpoint, region, bucket, prefix, accessKey, secretKey string, pathStyle bool) (*S3, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("backup.remote.endpoint must be https://host[:port], got %q", endpoint)
	}
	if u.Scheme == "http" {
		if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("backup.remote.endpoint must be https:// (http:// only to this machine)")
		}
	}
	if region == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("backup.remote needs region, bucket, access_key_id and secret_access_key_file")
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	u.Path = ""
	return &S3{Endpoint: u, Region: region, Bucket: bucket, Prefix: strings.TrimPrefix(prefix, "/"), AccessKey: accessKey, SecretKey: secretKey,
		PathStyle: pathStyle, Client: &http.Client{Timeout: 2 * time.Hour}, Now: time.Now}, nil
}

// Key es la clave del objeto para un archivo.
func (s *S3) Key(name string) string { return s.Prefix + name }

// Put sube body (de size bytes) como name. El cuerpo no se firma (UNSIGNED-PAYLOAD, válido por
// HTTPS) para no leer el respaldo dos veces; su integridad la da el cifrado (Decrypt falla si
// cambió un byte).
func (s *S3) Put(ctx context.Context, name string, body io.Reader, size int64) error {
	if size > MaxPut {
		return ErrTooLargeForPut
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(name), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Get baja el objeto name; quien llama cierra el cuerpo.
func (s *S3) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(name), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *S3) objectURL(name string) string {
	u := *s.Endpoint
	key := s.Key(name)
	if s.PathStyle {
		u.Path = "/" + s.Bucket + "/" + key
	} else {
		u.Host = s.Bucket + "." + u.Host
		u.Path = "/" + key
	}
	return u.String()
}

func (s *S3) do(req *http.Request) (*http.Response, error) {
	s.sign(req)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		// El cuerpo es un XML con el código de S3 (AccessDenied, NoSuchBucket…): sirve para el log.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("s3 %s %s: %s %s", req.Method, req.URL.Path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// sign aplica AWS Signature Version 4 (docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html).
func (s *S3) sign(req *http.Request) {
	now := s.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	payload := "UNSIGNED-PAYLOAD"
	if req.Body == nil {
		sum := sha256.Sum256(nil)
		payload = hex.EncodeToString(sum[:])
	}
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payload)

	names := make([]string, 0, len(req.Header))
	for k := range req.Header {
		names = append(names, strings.ToLower(k))
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(req.Header.Get(k)) + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{req.Method, req.URL.EscapedPath(), canonicalQuery(req.URL.Query()), canonHeaders.String(), signed, payload}, "\n")
	scope := day + "/" + s.Region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := hmacSHA256([]byte("AWS4"+s.SecretKey), day)
	key = hmacSHA256(key, s.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	req.Header.Del("Host") // net/http lo pone desde req.Host; firmado ya está
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+strings.ReplaceAll(url.QueryEscape(v), "+", "%20"))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
