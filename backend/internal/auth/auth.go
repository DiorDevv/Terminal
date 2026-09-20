package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrLocked             = errors.New("too many failed attempts, try again later")
	ErrSessionInvalid     = errors.New("invalid or expired session")
)

const (
	// A session dies after sessionIdle without use, and after sessionMax no
	// matter how active it is, so a stolen cookie has a bounded lifetime.
	sessionIdle = 8 * time.Hour
	sessionMax  = 7 * 24 * time.Hour

	// touchInterval limits how often last_seen is written, so a busy page
	// polling the API doesn't turn every request into a DB write.
	touchInterval = time.Minute

	maxFailedLogins = 5
	lockDuration    = 15 * time.Minute
)

// User is a panel account (not a squid proxy user).
type User struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	Role               Role      `json:"role"`
	Disabled           bool      `json:"disabled"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
}

// Session is one logged-in browser.
type Session struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Current   bool      `json:"current"`
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// dummyHash is compared against when the username doesn't exist, so a login
// for an unknown user takes as long as one for a real user and the response
// time can't be used to enumerate accounts.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("squidadmin-dummy-password"), bcrypt.DefaultCost)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

const userColumns = `id, username, role, disabled, must_change_password, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanUser(s scanner) (User, error) {
	var u User
	var role string
	var disabled, must int
	var created sql.NullString
	if err := s.Scan(&u.ID, &u.Username, &role, &disabled, &must, &created); err != nil {
		return User{}, err
	}
	u.Role = Role(role)
	u.Disabled = disabled != 0
	u.MustChangePassword = must != 0
	u.CreatedAt = parseDBTime(created)
	return u, nil
}

// parseDBTime reads a created_at column. SQLite stores CURRENT_TIMESTAMP as
// "YYYY-MM-DD HH:MM:SS", but the driver may hand it back already converted to
// RFC 3339, so both forms are accepted.
func parseDBTime(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02 15:04:05", v.String); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, v.String); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// Bootstrap creates the first admin account when the panel has no users at
// all. It never touches an existing installation, so an admin's changed
// password (or a deleted default account) survives restarts.
func (s *Service) Bootstrap(username, password string, mustChange bool) (created bool, err error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}

	hash, err := hashPassword(password)
	if err != nil {
		return false, err
	}
	_, err = s.db.Exec(
		`INSERT INTO users (username, password_hash, role, must_change_password) VALUES (?, ?, 'admin', ?)`,
		username, hash, boolInt(mustChange))
	return err == nil, err
}

// FlagWeakPasswords forces a password change for every account whose password
// is one of the well-known defaults. It catches installations created before
// the panel stopped shipping a default password.
func (s *Service) FlagWeakPasswords(known ...string) (flagged int, err error) {
	rows, err := s.db.Query(`SELECT id, password_hash FROM users WHERE must_change_password = 0`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id   int64
		hash string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.hash); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()

	for _, r := range todo {
		for _, pw := range known {
			if bcrypt.CompareHashAndPassword([]byte(r.hash), []byte(pw)) == nil {
				if _, err := s.db.Exec(`UPDATE users SET must_change_password = 1 WHERE id = ?`, r.id); err != nil {
					return flagged, err
				}
				flagged++
				break
			}
		}
	}
	return flagged, nil
}

// Login checks the credentials and opens a session. It returns the opaque
// session token to put in the cookie. After maxFailedLogins wrong passwords
// the account is locked for lockDuration.
func (s *Service) Login(username, password, ip, userAgent string) (string, User, error) {
	var (
		id       int64
		hash     string
		disabled int
		failed   int
		locked   int64
	)
	err := s.db.QueryRow(
		`SELECT id, password_hash, disabled, failed_logins, locked_until FROM users WHERE username = ?`, username,
	).Scan(&id, &hash, &disabled, &failed, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}

	now := s.now()
	if locked > now.Unix() {
		return "", User{}, ErrLocked
	}

	ok := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	if !ok || disabled != 0 {
		if !ok {
			failed++
			lockUntil := int64(0)
			if failed >= maxFailedLogins {
				lockUntil = now.Add(lockDuration).Unix()
				failed = 0
			}
			s.db.Exec(`UPDATE users SET failed_logins = ?, locked_until = ? WHERE id = ?`, failed, lockUntil, id)
		}
		return "", User{}, ErrInvalidCredentials
	}

	if failed != 0 || locked != 0 {
		s.db.Exec(`UPDATE users SET failed_logins = 0, locked_until = 0 WHERE id = ?`, id)
	}

	user, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if err != nil {
		return "", User{}, err
	}

	token, err := newToken()
	if err != nil {
		return "", User{}, err
	}
	s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	_, err = s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, created_at, last_seen, expires_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(token), id, now.Unix(), now.Unix(), now.Add(sessionMax).Unix(), ip, truncate(userAgent, 200))
	if err != nil {
		return "", User{}, err
	}
	return token, user, nil
}

// Authenticate resolves a session token to its user, enforcing idle and
// absolute expiry and that the account is still enabled.
func (s *Service) Authenticate(token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrSessionInvalid
	}

	var (
		sess                   Session
		created, seen, expires int64
		uid                    int64
		username, role         string
		disabled, must         int
		userCreated            sql.NullString
	)
	err := s.db.QueryRow(
		`SELECT s.id, s.created_at, s.last_seen, s.expires_at, s.ip, s.user_agent,
		        u.id, u.username, u.role, u.disabled, u.must_change_password, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?`, hashToken(token),
	).Scan(&sess.ID, &created, &seen, &expires, &sess.IP, &sess.UserAgent,
		&uid, &username, &role, &disabled, &must, &userCreated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, ErrSessionInvalid
	}
	if err != nil {
		return User{}, Session{}, err
	}

	now := s.now()
	if expires <= now.Unix() || seen+int64(sessionIdle/time.Second) <= now.Unix() || disabled != 0 {
		s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sess.ID)
		return User{}, Session{}, ErrSessionInvalid
	}

	if now.Unix()-seen >= int64(touchInterval/time.Second) {
		s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE id = ?`, now.Unix(), sess.ID)
		seen = now.Unix()
	}

	sess.CreatedAt = time.Unix(created, 0).UTC()
	sess.LastSeen = time.Unix(seen, 0).UTC()
	sess.ExpiresAt = time.Unix(expires, 0).UTC()
	sess.Current = true

	user := User{
		ID: uid, Username: username, Role: Role(role),
		Disabled: disabled != 0, MustChangePassword: must != 0,
		CreatedAt: parseDBTime(userCreated),
	}
	return user, sess, nil
}

// Logout ends the session behind token. Unknown tokens are ignored.
func (s *Service) Logout(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// Sessions lists a user's active sessions; the one belonging to currentToken
// is flagged so the UI can show "this device".
func (s *Service) Sessions(userID int64, currentToken string) ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, token_hash, created_at, last_seen, expires_at, ip, user_agent
		 FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen DESC`,
		userID, s.now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cur := hashToken(currentToken)
	out := []Session{}
	for rows.Next() {
		var sess Session
		var th string
		var created, seen, expires int64
		if err := rows.Scan(&sess.ID, &th, &created, &seen, &expires, &sess.IP, &sess.UserAgent); err != nil {
			return nil, err
		}
		sess.CreatedAt = time.Unix(created, 0).UTC()
		sess.LastSeen = time.Unix(seen, 0).UTC()
		sess.ExpiresAt = time.Unix(expires, 0).UTC()
		sess.Current = th == cur
		out = append(out, sess)
	}
	return out, rows.Err()
}

// RevokeSession ends one of the user's own sessions.
func (s *Service) RevokeSession(userID, sessionID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	return err
}

// ChangePassword verifies the current password before setting a new one, so a
// stolen session alone can't lock the real owner out. All the user's other
// sessions are ended (the caller's stays), and a forced-change flag is cleared.
func (s *Service) ChangePassword(userID int64, oldPassword, newPassword, currentToken string) error {
	var username, hash string
	err := s.db.QueryRow(`SELECT username, password_hash FROM users WHERE id = ?`, userID).Scan(&username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)) != nil {
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(username, newPassword); err != nil {
		return err
	}
	if oldPassword == newPassword {
		return errors.New("new password must differ from the current one")
	}

	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?`, newHash, userID); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, hashToken(currentToken))
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
