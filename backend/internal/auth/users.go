package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrUserExists  = errors.New("a user with that name already exists")
	ErrUserMissing = errors.New("user not found")
	// ErrLastAdmin protects against locking everyone out of the panel.
	ErrLastAdmin = errors.New("cannot remove, disable or demote the last active admin")
)

var panelUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

func (s *Service) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) getUser(id int64) (User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserMissing
	}
	return u, err
}

// CreateUser adds a panel account. The initial password must be changed at
// first login, so whoever creates the account never knows the final one.
func (s *Service) CreateUser(username, password string, role Role) (User, error) {
	username = strings.TrimSpace(username)
	if !panelUsernamePattern.MatchString(username) {
		return User{}, errors.New("invalid username: only letters, digits, '.', '_', '-' allowed, 3-32 chars")
	}
	if !role.Valid() {
		return User{}, fmt.Errorf("invalid role %q", role)
	}
	if err := ValidatePassword(username, password); err != nil {
		return User{}, err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}

	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, role, must_change_password) VALUES (?, ?, ?, 1)`,
		username, hash, string(role))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrUserExists
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return s.getUser(id)
}

// activeAdmins counts enabled admins other than exceptID.
func (s *Service) activeAdminsExcept(exceptID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0 AND id != ?`, exceptID).Scan(&n)
	return n, err
}

// UpdateUser changes a user's role and/or enabled state. Disabling an account
// ends all its sessions immediately.
func (s *Service) UpdateUser(id int64, role *Role, disabled *bool) (User, error) {
	cur, err := s.getUser(id)
	if err != nil {
		return User{}, err
	}

	newRole := cur.Role
	if role != nil {
		if !role.Valid() {
			return User{}, fmt.Errorf("invalid role %q", *role)
		}
		newRole = *role
	}
	newDisabled := cur.Disabled
	if disabled != nil {
		newDisabled = *disabled
	}

	losesAdmin := cur.Role == RoleAdmin && !cur.Disabled && (newRole != RoleAdmin || newDisabled)
	if losesAdmin {
		others, err := s.activeAdminsExcept(id)
		if err != nil {
			return User{}, err
		}
		if others == 0 {
			return User{}, ErrLastAdmin
		}
	}

	if _, err := s.db.Exec(`UPDATE users SET role = ?, disabled = ? WHERE id = ?`,
		string(newRole), boolInt(newDisabled), id); err != nil {
		return User{}, err
	}
	if newDisabled || newRole != cur.Role {
		// A role change must not keep running on the old permissions.
		s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	}
	return s.getUser(id)
}

// ResetPassword sets a new temporary password, forces a change at next login
// and signs the user out everywhere.
func (s *Service) ResetPassword(id int64, newPassword string) error {
	u, err := s.getUser(id)
	if err != nil {
		return err
	}
	if err := ValidatePassword(u.Username, newPassword); err != nil {
		return err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`UPDATE users SET password_hash = ?, must_change_password = 1, failed_logins = 0, locked_until = 0 WHERE id = ?`,
		hash, id); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	return err
}

func (s *Service) DeleteUser(id int64) error {
	u, err := s.getUser(id)
	if err != nil {
		return err
	}
	if u.Role == RoleAdmin && !u.Disabled {
		others, err := s.activeAdminsExcept(id)
		if err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	_, err = s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}
