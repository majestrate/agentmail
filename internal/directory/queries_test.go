package directory

import (
	"strings"
	"testing"
)

func TestEmbeddedQueriesAreStatementNames(t *testing.T) {
	entries, err := sqlFiles.ReadDir("sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded queries")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".sql")
		if strings.ContainsAny(name, " \t\n") {
			t.Fatalf("%s is not a prepared statement name", entry.Name())
		}
		body, err := sqlFiles.ReadFile("sql/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(body)) == "" {
			t.Fatalf("%s is empty", entry.Name())
		}
		seen[name] = true
	}
	for _, name := range []string{
		stmtCreateUser, stmtGetUserByID, stmtGetUserByAddress, stmtListUsers,
		stmtUpdateUser, stmtDeleteUser, stmtCreateList, stmtGetListByID,
		stmtGetListByAddress, stmtListLists, stmtUpdateList, stmtDeleteList,
		stmtAddMember, stmtRemoveMember, stmtLockList, stmtCountUsers,
		stmtClearMembers, stmtListMembers, stmtListUserMemberships,
		stmtGetMembership, stmtListExists, stmtUserExists,
	} {
		if !seen[name] {
			t.Fatalf("missing embedded query %s", name)
		}
	}
}
