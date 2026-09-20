package auth

// Role controls what a panel user may do. Roles are ordered: every role can
// do everything the roles below it can.
//
//	viewer   — read-only dashboard and lists
//	operator — day-to-day proxy management (block lists, restrictions, groups,
//	           proxy users, LAN access, reload, live logs)
//	admin    — everything, including the raw config editor, config history,
//	           stopping/restarting squid, panel users and the audit log
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

func (r Role) Valid() bool { return r.rank() > 0 }

// AtLeast reports whether r grants at least the permissions of min.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() && r.Valid() }
