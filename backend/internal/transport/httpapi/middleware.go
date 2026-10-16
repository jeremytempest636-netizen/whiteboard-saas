package httpapi

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// statusRecorder captures the response status for logging. It must keep the
// http.Hijacker interface or WebSocket upgrades would fail.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpapi: underlying writer cannot hijack")
	}
	s.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// recoverer turns panics into 500 responses instead of killing the process.
func recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic in handler", zap.Any("panic", rec), zap.String("path", r.URL.Path), zap.Stack("stack"))
					writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// accessLog writes one structured line per request.
func accessLog(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			if r.URL.Path == "/healthz" {
				return
			}
			log.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rec.status),
				zap.Duration("took", time.Since(start)),
			)
		})
	}
}

// securityHeaders adds conservative defaults to API responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// cors allows the configured browser origins to call the API.
func cors(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}
	_, any := set["*"]
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := set[origin]; ok || any {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Add("Vary", "Origin")
					h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					h.Set("Access-Control-Max-Age", "600")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ipLimiter is a per-client-IP token bucket for the endpoints that create rows.
type ipLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	clients map[string]*visitor
	trust   bool
	calls   int
}

type visitor struct {
	tokens float64
	seen   time.Time
}

func newIPLimiter(perMinute, burst int, trustProxy bool) *ipLimiter {
	return &ipLimiter{
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
		clients: make(map[string]*visitor),
		trust:   trustProxy,
	}
}

func (l *ipLimiter) clientIP(r *http.Request) string {
	if l.trust {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *ipLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%512 == 0 { // opportunistic cleanup of idle entries
		for k, v := range l.clients {
			if now.Sub(v.seen) > 10*time.Minute {
				delete(l.clients, k)
			}
		}
	}
	v, ok := l.clients[ip]
	if !ok {
		v = &visitor{tokens: l.burst, seen: now}
		l.clients[ip] = v
	}
	v.tokens += now.Sub(v.seen).Seconds() * l.rate
	if v.tokens > l.burst {
		v.tokens = l.burst
	}
	v.seen = now
	if v.tokens < 1 {
		return false
	}
	v.tokens--
	return true
}

func (l *ipLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(l.clientIP(r)) {
			w.Header().Set("Retry-After", "10")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, try again shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

// principal is the authenticated caller attached to the request context.
type principal struct {
	UserID string
	Name   string
}

func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(principal)
	return p, ok
}
