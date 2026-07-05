package store

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrDuplicateUsername = errors.New("username already exists")
	ErrDuplicateEmail    = errors.New("email already exists")
)
