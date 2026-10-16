// Package httpapi exposes the REST endpoints and the WebSocket upgrade route.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"whiteboard/internal/auth"
	"whiteboard/internal/domain"
	"whiteboard/internal/service"
	"whiteboard/internal/websocket"
)

// Pinger is anything with a health check (database pool, Redis client).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Log            *zap.Logger
	Auth           *service.Auth
	Boards         *service.Boards
	Hub            *websocket.Hub
	AllowedOrigins []string
	TrustProxy     bool
	Ready          map[string]Pinger // checked by /readyz
}

// Server wires routes and middleware.
type Server struct {
	Deps
	draining chan struct{}
}

// New builds the handler tree. The returned Server's BeginDrain flips /readyz
// to 503 so a load balancer stops sending new traffic before shutdown.
func New(d Deps) (*Server, http.Handler) {
	s := &Server{Deps: d, draining: make(chan struct{})}

	guestLimit := newIPLimiter(20, 10, d.TrustProxy)
	createLimit := newIPLimiter(60, 30, d.TrustProxy)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)

	mux.Handle("POST /api/auth/guest", guestLimit.middleware(http.HandlerFunc(s.guest)))
	mux.Handle("GET /api/me", s.authed(s.me))
	mux.Handle("POST /api/boards", createLimit.middleware(s.authed(s.createBoard)))
	mux.Handle("GET /api/boards", s.authed(s.listBoards))
	mux.Handle("GET /api/boards/{id}", s.authed(s.getBoard))
	mux.HandleFunc("GET /ws/boards/{id}", s.serveWS)

	return s, chain(mux, recoverer(d.Log), accessLog(d.Log), cors(d.AllowedOrigins), securityHeaders)
}

// BeginDrain marks the node as shutting down.
func (s *Server) BeginDrain() {
	select {
	case <-s.draining:
	default:
		close(s.draining)
	}
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "request body must be valid JSON")
		return false
	}
	return true
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrInvalid):
		msg := strings.TrimPrefix(err.Error(), service.ErrInvalid.Error()+": ")
		writeError(w, http.StatusUnprocessableEntity, "invalid", msg)
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	default:
		s.Log.Error("request failed", zap.String("path", r.URL.Path), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) authenticate(token string) (principal, error) {
	claims, err := s.Auth.Verify(token)
	if err != nil {
		return principal{}, err
	}
	return principal{UserID: claims.UserID, Name: claims.Name}, nil
}

// authed requires a valid bearer token.
func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(bearer(r))
		if err != nil {
			code := "unauthorized"
			if errors.Is(err, auth.ErrExpired) {
				code = "token_expired"
			}
			writeError(w, http.StatusUnauthorized, code, "sign in to continue")
			return
		}
		h(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// --- handlers --------------------------------------------------------------

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.draining:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
		return
	default:
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status, code := map[string]string{}, http.StatusOK
	for name, p := range s.Ready {
		if err := p.Ping(ctx); err != nil {
			status[name] = "down"
			code = http.StatusServiceUnavailable
		} else {
			status[name] = "up"
		}
	}
	writeJSON(w, code, map[string]any{"status": status, "hub": s.Hub.Stats()})
}

func (s *Server) guest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	sess, err := s.Auth.Guest(r.Context(), in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	u, err := s.Auth.User(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) createBoard(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Title string `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	b, err := s.Boards.Create(r.Context(), p.UserID, in.Title)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) listBoards(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	boards, err := s.Boards.ListMine(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (s *Server) getBoard(w http.ResponseWriter, r *http.Request) {
	b, err := s.Boards.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// serveWS authenticates with ?token= (browsers cannot set headers on a
// WebSocket handshake), checks the board exists and hands the connection to
// the hub.
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = bearer(r)
	}
	p, err := s.authenticate(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in to continue")
		return
	}
	board, err := s.Boards.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Hub.ServeWS(w, r, board.ID, websocket.Identity{UserID: p.UserID, Name: p.Name})
}
