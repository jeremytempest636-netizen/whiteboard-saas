// Package service holds the application use cases. It orchestrates the domain
// repositories and knows nothing about HTTP or WebSockets.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"whiteboard/internal/auth"
	"whiteboard/internal/domain"
)

// ErrInvalid marks input the caller can fix; its message is safe to show.
var ErrInvalid = errors.New("invalid input")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// cleanText trims, strips control characters and enforces a length in runes.
func cleanText(s string, min, max int) (string, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	n := utf8.RuneCountInString(s)
	return s, n >= min && n <= max
}

// Auth creates guest sessions and verifies tokens.
type Auth struct {
	users  domain.UserRepository
	signer *auth.Signer
	ttl    time.Duration
}

// NewAuth returns the authentication use cases.
func NewAuth(users domain.UserRepository, signer *auth.Signer, ttl time.Duration) *Auth {
	return &Auth{users: users, signer: signer, ttl: ttl}
}

// Session is what a client receives after signing in.
type Session struct {
	Token     string       `json:"token"`
	ExpiresAt int64        `json:"expiresAt"`
	User      *domain.User `json:"user"`
}

// Guest creates a user from a display name and returns a signed session.
func (a *Auth) Guest(ctx context.Context, name string) (*Session, error) {
	name, ok := cleanText(name, 1, 40)
	if !ok {
		return nil, invalid("name must be 1 to 40 characters")
	}
	u := &domain.User{DisplayName: name}
	if err := a.users.Create(ctx, u); err != nil {
		return nil, err
	}
	token, claims, err := a.signer.Issue(u.ID, u.DisplayName, a.ttl)
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, ExpiresAt: claims.Exp, User: u}, nil
}

// Verify checks a token.
func (a *Auth) Verify(token string) (auth.Claims, error) { return a.signer.Verify(token) }

// User loads a user by id.
func (a *Auth) User(ctx context.Context, id string) (*domain.User, error) {
	return a.users.GetByID(ctx, id)
}

// Boards implements the board use cases.
type Boards struct {
	boards domain.BoardRepository
}

// NewBoards returns the board use cases.
func NewBoards(boards domain.BoardRepository) *Boards { return &Boards{boards: boards} }

// Create makes a new board owned by ownerID.
func (b *Boards) Create(ctx context.Context, ownerID, title string) (*domain.Board, error) {
	if strings.TrimSpace(title) == "" {
		title = "Untitled board"
	}
	title, ok := cleanText(title, 1, 120)
	if !ok {
		return nil, invalid("title must be at most 120 characters")
	}
	board := &domain.Board{Title: title, OwnerID: ownerID}
	if err := b.boards.Create(ctx, board); err != nil {
		return nil, err
	}
	return board, nil
}

// Get returns a board by id.
func (b *Boards) Get(ctx context.Context, id string) (*domain.Board, error) {
	if len(id) == 0 || len(id) > 16 {
		return nil, domain.ErrNotFound
	}
	return b.boards.GetByID(ctx, id)
}

// ListMine returns the boards a user created, most recently active first.
func (b *Boards) ListMine(ctx context.Context, ownerID string) ([]domain.Board, error) {
	return b.boards.ListByOwner(ctx, ownerID, 50)
}

// keepSnapshots is how many historical snapshots are retained per board.
const keepSnapshots = 10

// Snapshots persists canvas documents. It satisfies websocket.SnapshotStore.
type Snapshots struct {
	snaps  domain.SnapshotRepository
	boards domain.BoardRepository
}

// NewSnapshots returns the snapshot use cases.
func NewSnapshots(snaps domain.SnapshotRepository, boards domain.BoardRepository) *Snapshots {
	return &Snapshots{snaps: snaps, boards: boards}
}

// Load returns the newest snapshot document, or (nil, nil) when none exists.
func (s *Snapshots) Load(ctx context.Context, boardID string) ([]byte, error) {
	snap, err := s.snaps.Latest(ctx, boardID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return snap.Data, nil
}

// Save stores a snapshot, bumps the board's activity time and trims history.
func (s *Snapshots) Save(ctx context.Context, boardID string, data []byte, elements int) error {
	snap := &domain.BoardSnapshot{BoardID: boardID, Data: domain.JSONB(data), ElementCount: elements}
	if err := s.snaps.Save(ctx, snap); err != nil {
		return err
	}
	// Housekeeping failures must not fail the save: the snapshot is already safe.
	_ = s.boards.Touch(ctx, boardID)
	_ = s.snaps.Prune(ctx, boardID, keepSnapshots)
	return nil
}
