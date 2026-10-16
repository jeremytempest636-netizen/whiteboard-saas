package domain

import "context"

// UserRepository persists users.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
}

// BoardRepository persists boards.
type BoardRepository interface {
	Create(ctx context.Context, b *Board) error
	// GetByID reads from the primary so a freshly created board is visible
	// immediately, before replication catches up.
	GetByID(ctx context.Context, id string) (*Board, error)
	// ListByOwner may be served by a read replica.
	ListByOwner(ctx context.Context, ownerID string, limit int) ([]Board, error)
	Touch(ctx context.Context, id string) error
}

// SnapshotRepository persists canvas snapshots.
type SnapshotRepository interface {
	Save(ctx context.Context, s *BoardSnapshot) error
	// Latest reads from the primary; a stale snapshot would lose edits.
	Latest(ctx context.Context, boardID string) (*BoardSnapshot, error)
	// Prune deletes all but the newest keep snapshots of a board.
	Prune(ctx context.Context, boardID string, keep int) error
}
