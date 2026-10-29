package middleware

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limit es el límite general de la API: fichas por segundo y cuántas se pueden juntar.
type Limit struct {
	PerSecond float64
	Burst     int
}

// DefaultLimit [PROPUESTA, SPEC §14.5]: holgado para la SPA (una página pide varias cosas a la
// vez) y suficiente para frenar una inundación. El login tiene además su propio límite (§8.2).
var DefaultLimit = Limit{PerSecond: 30, Burst: 300}

// idleBucket: un balde sin uso por este tiempo se olvida, para que el mapa no crezca sin fin.
const idleBucket = 10 * time.Minute

type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimit aplica el límite por IP de la conexión (SPEC §14.5). Detrás de un proxy inverso
// todas las peticiones traen la IP del proxy: la IP real llega con trust_proxy (F8, §21.2).
func RateLimit(l Limit, now func() time.Time) func(http.Handler) http.Handler {
	var mu sync.Mutex
	buckets := map[string]*bucket{}
	swept := now()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			t := now()
			mu.Lock()
			if t.Sub(swept) > time.Minute {
				for k, b := range buckets {
					if t.Sub(b.last) > idleBucket {
						delete(buckets, k)
					}
				}
				swept = t
			}
			b := buckets[ip]
			if b == nil {
				b = &bucket{tokens: float64(l.Burst), last: t}
				buckets[ip] = b
			}
			b.tokens = math.Min(float64(l.Burst), b.tokens+t.Sub(b.last).Seconds()*l.PerSecond)
			b.last = t
			ok := b.tokens >= 1
			if ok {
				b.tokens--
			}
			wait := (1 - b.tokens) / l.PerSecond
			mu.Unlock()
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait))))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":{"code":"rate_limited","message":"Too Many Requests"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
