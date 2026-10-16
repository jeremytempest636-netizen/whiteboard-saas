// Package domain defines the core entities and the ports (repository
// interfaces) the rest of the system depends on. It knows nothing about HTTP,
// WebSockets or Redis.
package domain

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// User is a person who can open boards. Sign-in is by display name (guest
// accounts); the table is shaped so email/OAuth columns can be added later.
type User struct {
	ID          string    `gorm:"type:uuid;primaryKey" json:"id"`
	DisplayName string    `gorm:"size:60;not null" json:"displayName"`
	CreatedAt   time.Time `gorm:"not null;autoCreateTime" json:"createdAt"`
}

// Board is a shared canvas. Anyone holding the link and a session may join.
type Board struct {
	ID        string    `gorm:"size:16;primaryKey" json:"id"`
	Title     string    `gorm:"size:120;not null" json:"title"`
	OwnerID   string    `gorm:"type:uuid;not null;index" json:"ownerId"`
	Owner     *User     `gorm:"foreignKey:OwnerID;constraint:OnDelete:CASCADE" json:"-"`
	CreatedAt time.Time `gorm:"not null;autoCreateTime" json:"createdAt"`
	UpdatedAt time.Time `gorm:"not null;autoUpdateTime" json:"updatedAt"`
}

// BoardSnapshot is a point-in-time copy of a board's canvas as JSONB. Several
// are kept per board (newest wins on load) so a bad write can be rolled back.
type BoardSnapshot struct {
	ID           string    `gorm:"type:uuid;primaryKey" json:"id"`
	BoardID      string    `gorm:"size:16;not null;index:idx_snapshots_board_created,priority:1" json:"boardId"`
	Board        *Board    `gorm:"foreignKey:BoardID;constraint:OnDelete:CASCADE" json:"-"`
	Data         JSONB     `gorm:"type:jsonb;not null" json:"data"`
	ElementCount int       `gorm:"not null;default:0" json:"elementCount"`
	CreatedAt    time.Time `gorm:"not null;autoCreateTime;index:idx_snapshots_board_created,priority:2,sort:desc" json:"createdAt"`
}

// ErrNotFound is returned by repositories when a row does not exist.
var ErrNotFound = errors.New("not found")

// JSONB maps a Go byte slice to a Postgres jsonb column.
type JSONB json.RawMessage

// GormDataType tells GORM which column type to use.
func (JSONB) GormDataType() string { return "jsonb" }

// Value implements driver.Valuer.
func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner.
func (j *JSONB) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append((*j)[:0], v...)
	case string:
		*j = append((*j)[:0], v...)
	default:
		return fmt.Errorf("domain: cannot scan %T into JSONB", src)
	}
	return nil
}

// MarshalJSON emits the stored document as-is.
func (j JSONB) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON stores the raw document.
func (j *JSONB) UnmarshalJSON(b []byte) error {
	*j = append((*j)[:0], b...)
	return nil
}

// NewUUID returns a random RFC 4122 version 4 UUID.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

const boardAlphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no look-alike characters

// NewBoardID returns a 10 character, URL-friendly board id (~49 bits).
func NewBoardID() string {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = boardAlphabet[int(b)%len(boardAlphabet)]
	}
	return string(out)
}
