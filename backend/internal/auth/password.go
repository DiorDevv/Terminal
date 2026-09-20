package auth

import (
	"errors"
	"fmt"
	"strings"
)

const (
	minPasswordLen = 8
	// bcrypt silently ignores everything after 72 bytes, so a longer password
	// would be weaker than it looks.
	maxPasswordBytes = 72
)

// commonPasswords are rejected outright. This is a short guard against the
// obvious defaults, not a full breached-password check.
var commonPasswords = map[string]bool{
	"admin123": true, "administrator": true, "password": true, "password1": true,
	"password123": true, "12345678": true, "123456789": true, "1234567890": true,
	"qwerty123": true, "qwertyuiop": true, "changeme": true, "change-this-password": true,
	"letmein123": true, "welcome123": true, "squidadmin": true, "iloveyou": true,
}

var ErrWeakPassword = errors.New("weak password")

// ValidatePassword enforces the panel's password policy.
func ValidatePassword(username, password string) error {
	switch {
	case len(password) < minPasswordLen:
		return fmt.Errorf("%w: must be at least %d characters", ErrWeakPassword, minPasswordLen)
	case len(password) > maxPasswordBytes:
		return fmt.Errorf("%w: must be at most %d bytes", ErrWeakPassword, maxPasswordBytes)
	case strings.EqualFold(password, username):
		return fmt.Errorf("%w: must not be the same as the username", ErrWeakPassword)
	case commonPasswords[strings.ToLower(password)]:
		return fmt.Errorf("%w: too common, pick something less guessable", ErrWeakPassword)
	}
	return nil
}
