package directory

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

// Statement names. pgx treats a query argument with no spaces as a prepared
// statement name, so these must match the embedded file names.
const (
	stmtCreateUser          = "create_user"
	stmtGetUserByID         = "get_user_by_id"
	stmtGetUserByAddress    = "get_user_by_address"
	stmtListUsers           = "list_users"
	stmtUpdateUser          = "update_user"
	stmtDeleteUser          = "delete_user"
	stmtCreateList          = "create_list"
	stmtGetListByID         = "get_list_by_id"
	stmtGetListByAddress    = "get_list_by_address"
	stmtListLists           = "list_lists"
	stmtUpdateList          = "update_list"
	stmtDeleteList          = "delete_list"
	stmtAddMember           = "add_member"
	stmtRemoveMember        = "remove_member"
	stmtLockList            = "lock_list"
	stmtCountUsers          = "count_users"
	stmtClearMembers        = "clear_members"
	stmtListMembers         = "list_members"
	stmtListUserMemberships = "list_user_memberships"
	stmtGetMembership       = "get_membership"
	stmtListExists          = "list_exists"
	stmtUserExists          = "user_exists"
)

// PrepareQueries prepares every embedded query on conn. Call it from the pool
// AfterConnect hook so every connection has the statements before use.
func PrepareQueries(ctx context.Context, conn *pgx.Conn) error {
	entries, err := sqlFiles.ReadDir("sql")
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no embedded sql queries")
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".sql")
		if strings.Contains(name, " ") || !strings.HasSuffix(entry.Name(), ".sql") {
			return fmt.Errorf("sql file %s cannot be a prepared statement name", entry.Name())
		}
		body, err := sqlFiles.ReadFile("sql/" + entry.Name())
		if err != nil {
			return err
		}
		sqlText := strings.TrimSpace(string(body))
		if sqlText == "" {
			return fmt.Errorf("sql file %s is empty", entry.Name())
		}
		if _, err := conn.Prepare(ctx, name, sqlText); err != nil {
			return fmt.Errorf("prepare %s: %w", name, err)
		}
	}
	return nil
}
