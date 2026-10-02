// Package password keeps the password that protects changes to a hub's list
// of devices. It is chosen when the first device is added and stays the same
// after that. Only a salted hash is stored.
package password

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"sync"
	"time"
	"unicode/utf8"
)

// MinLength and MaxLength limit the length of a new password, in characters.
const (
	MinLength = 8
	MaxLength = 128
)

const (
	// iterations is how often the hash is repeated, so guessing is slow even
	// with a copy of the database (OWASP's recommendation for PBKDF2-SHA256).
	iterations = 600_000
	saltBytes  = 16
	keyBytes   = 32
	// failureDelay is how long a wrong password is answered late, so a
	// password cannot be guessed quickly over the network.
	failureDelay = time.Second
)

var (
	// ErrWrong is returned for a password that does not match.
	ErrWrong = errors.New("wrong password")
	// ErrLength is returned for a new password that is too short or too long.
	ErrLength = errors.New("the password must have 8 to 128 characters")
	// ErrNotSet is returned when no password has been chosen yet.
	ErrNotSet = errors.New("no password has been chosen yet")
	// ErrAlreadySet is returned when a password is chosen a second time.
	ErrAlreadySet = errors.New("the password has been chosen already")
)

const schema = `
CREATE TABLE IF NOT EXISTS password (
	id   INTEGER PRIMARY KEY CHECK (id = 1), -- there is only one password
	salt BLOB NOT NULL,
	hash BLOB NOT NULL
);
`

// Password is the stored password.
type Password struct {
	db *sql.DB

	// mu lets one check run at a time, so wrong guesses wait for each other.
	mu sync.Mutex
}

// Open returns the password kept in db, creating its table when needed.
func Open(ctx context.Context, db *sql.DB) (*Password, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, err
	}
	return &Password{db: db}, nil
}

// IsSet reports whether a password has been chosen.
func (p *Password) IsSet(ctx context.Context) (bool, error) {
	var count int
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM password`).Scan(&count)
	return count > 0, err
}

// Check checks password against the stored one. It returns ErrNotSet when
// none has been chosen yet.
func (p *Password) Check(ctx context.Context, password string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var salt, hash []byte
	err := p.db.QueryRowContext(ctx, `SELECT salt, hash FROM password WHERE id = 1`).Scan(&salt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotSet
	}
	if err != nil {
		return err
	}

	given, err := derive(password, salt)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(given, hash) != 1 {
		time.Sleep(failureDelay)
		return ErrWrong
	}
	return nil
}

// Set chooses the password. It returns ErrAlreadySet when one has been chosen
// before, since it never changes, and ErrLength when password is too short
// or too long.
func (p *Password) Set(ctx context.Context, password string) error {
	if err := CheckNew(password); err != nil {
		return err
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash, err := derive(password, salt)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	result, err := p.db.ExecContext(ctx, `INSERT OR IGNORE INTO password (id, salt, hash) VALUES (1, ?, ?)`, salt, hash)
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 0 {
		return errors.Join(ErrAlreadySet, err)
	}
	return nil
}

// CheckNew returns ErrLength when password is too short or too long to be chosen.
func CheckNew(password string) error {
	if length := utf8.RuneCountInString(password); length < MinLength || length > MaxLength {
		return ErrLength
	}
	return nil
}

// Reset deletes the password, so the next change chooses a new one.
func (p *Password) Reset(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM password`)
	return err
}

func derive(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, iterations, keyBytes)
}
