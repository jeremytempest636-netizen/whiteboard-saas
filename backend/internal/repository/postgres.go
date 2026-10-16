// Package repository implements the domain ports on top of GORM.
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"

	"whiteboard/internal/domain"
)

// primary forces a query onto the write database.
func primary(db *gorm.DB, ctx context.Context) *gorm.DB {
	return db.WithContext(ctx).Clauses(dbresolver.Write)
}

func mapErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}
	return err
}

// Users -----------------------------------------------------------------

type userRepo struct{ db *gorm.DB }

// NewUserRepository returns a UserRepository.
func NewUserRepository(db *gorm.DB) domain.UserRepository { return &userRepo{db} }

func (r *userRepo) Create(ctx context.Context, u *domain.User) error {
	if u.ID == "" {
		u.ID = domain.NewUUID()
	}
	return r.db.WithContext(ctx).Create(u).Error
}

func (r *userRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	var u domain.User
	if err := primary(r.db, ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

// Boards ----------------------------------------------------------------

type boardRepo struct{ db *gorm.DB }

// NewBoardRepository returns a BoardRepository.
func NewBoardRepository(db *gorm.DB) domain.BoardRepository { return &boardRepo{db} }

func (r *boardRepo) Create(ctx context.Context, b *domain.Board) error {
	if b.ID == "" {
		b.ID = domain.NewBoardID()
	}
	return r.db.WithContext(ctx).Create(b).Error
}

func (r *boardRepo) GetByID(ctx context.Context, id string) (*domain.Board, error) {
	var b domain.Board
	if err := primary(r.db, ctx).First(&b, "id = ?", id).Error; err != nil {
		return nil, mapErr(err)
	}
	return &b, nil
}

// ListByOwner is a plain SELECT, so the resolver sends it to a replica.
func (r *boardRepo) ListByOwner(ctx context.Context, ownerID string, limit int) ([]domain.Board, error) {
	var out []domain.Board
	err := r.db.WithContext(ctx).
		Where("owner_id = ?", ownerID).
		Order("updated_at DESC").
		Limit(limit).
		Find(&out).Error
	return out, err
}

func (r *boardRepo) Touch(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).
		Model(&domain.Board{}).
		Where("id = ?", id).
		UpdateColumn("updated_at", gorm.Expr("NOW()")).Error
}

// Snapshots -------------------------------------------------------------

type snapshotRepo struct{ db *gorm.DB }

// NewSnapshotRepository returns a SnapshotRepository.
func NewSnapshotRepository(db *gorm.DB) domain.SnapshotRepository { return &snapshotRepo{db} }

func (r *snapshotRepo) Save(ctx context.Context, s *domain.BoardSnapshot) error {
	if s.ID == "" {
		s.ID = domain.NewUUID()
	}
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *snapshotRepo) Latest(ctx context.Context, boardID string) (*domain.BoardSnapshot, error) {
	var s domain.BoardSnapshot
	err := primary(r.db, ctx).
		Where("board_id = ?", boardID).
		Order("created_at DESC").
		First(&s).Error
	if err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}

func (r *snapshotRepo) Prune(ctx context.Context, boardID string, keep int) error {
	return r.db.WithContext(ctx).Exec(
		`DELETE FROM board_snapshots
		  WHERE board_id = ?
		    AND id NOT IN (
		      SELECT id FROM board_snapshots
		       WHERE board_id = ?
		       ORDER BY created_at DESC
		       LIMIT ?)`,
		boardID, boardID, keep,
	).Error
}
