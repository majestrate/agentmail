package directory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)

type User struct {
	ID          int64
	Address     string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ListCount   int64
}

type DistributionList struct {
	ID          int64
	Address     string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MemberCount int64
}

type Membership struct {
	ListID          int64
	UserID          int64
	ListAddress     string
	UserAddress     string
	UserDisplayName string
	CreatedAt       time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) CreateUser(ctx context.Context, address, displayName, passwordHash string) (User, error) {
	var id int64
	err := s.pool.QueryRow(ctx, stmtCreateUser, address, displayName, passwordHash).Scan(&id)
	if err != nil {
		return User{}, mapPg(err)
	}
	return s.GetUser(ctx, id, "")
}

func (s *Store) GetUser(ctx context.Context, id int64, address string) (User, error) {
	stmt, arg := stmtGetUserByID, any(id)
	if address != "" {
		stmt, arg = stmtGetUserByAddress, address
	}
	user, err := scanUser(s.pool.QueryRow(ctx, stmt, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) ListUsers(ctx context.Context, after int64, limit int) ([]User, error) {
	rows, err := s.pool.Query(ctx, stmtListUsers, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectUsers(rows)
}

func (s *Store) UpdateUser(ctx context.Context, id int64, address, displayName, passwordHash *string) (User, error) {
	if address == nil && displayName == nil && passwordHash == nil {
		return User{}, fmt.Errorf("%w: no fields to update", ErrInvalid)
	}
	tag, err := s.pool.Exec(ctx, stmtUpdateUser,
		id,
		address != nil, deref(address),
		displayName != nil, deref(displayName),
		passwordHash != nil, deref(passwordHash),
	)
	if err != nil {
		return User{}, mapPg(err)
	}
	if tag.RowsAffected() == 0 {
		return User{}, ErrNotFound
	}
	return s.GetUser(ctx, id, "")
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, stmtDeleteUser, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateDistributionList(ctx context.Context, address, displayName string) (DistributionList, error) {
	var id int64
	err := s.pool.QueryRow(ctx, stmtCreateList, address, displayName).Scan(&id)
	if err != nil {
		return DistributionList{}, mapPg(err)
	}
	return s.GetDistributionList(ctx, id, "")
}

func (s *Store) GetDistributionList(ctx context.Context, id int64, address string) (DistributionList, error) {
	stmt, arg := stmtGetListByID, any(id)
	if address != "" {
		stmt, arg = stmtGetListByAddress, address
	}
	list, err := scanList(s.pool.QueryRow(ctx, stmt, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return DistributionList{}, ErrNotFound
	}
	return list, err
}

func (s *Store) ListDistributionLists(ctx context.Context, after int64, limit int) ([]DistributionList, error) {
	rows, err := s.pool.Query(ctx, stmtListLists, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectLists(rows)
}

func (s *Store) UpdateDistributionList(ctx context.Context, id int64, address, displayName *string) (DistributionList, error) {
	if address == nil && displayName == nil {
		return DistributionList{}, fmt.Errorf("%w: no fields to update", ErrInvalid)
	}
	tag, err := s.pool.Exec(ctx, stmtUpdateList, id, address != nil, deref(address), displayName != nil, deref(displayName))
	if err != nil {
		return DistributionList{}, mapPg(err)
	}
	if tag.RowsAffected() == 0 {
		return DistributionList{}, ErrNotFound
	}
	return s.GetDistributionList(ctx, id, "")
}

func (s *Store) DeleteDistributionList(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, stmtDeleteList, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddMember(ctx context.Context, listID, userID int64) (Membership, error) {
	if _, err := s.pool.Exec(ctx, stmtAddMember, listID, userID); err != nil {
		return Membership{}, mapPg(err)
	}
	return s.getMembership(ctx, listID, userID)
}

func (s *Store) RemoveMember(ctx context.Context, listID, userID int64) error {
	tag, err := s.pool.Exec(ctx, stmtRemoveMember, listID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetMembers(ctx context.Context, listID int64, userIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists int64
	err = tx.QueryRow(ctx, stmtLockList, listID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if len(userIDs) > 0 {
		var found int
		err = tx.QueryRow(ctx, stmtCountUsers, userIDs).Scan(&found)
		if err != nil {
			return err
		}
		if found != len(userIDs) {
			return fmt.Errorf("%w: unknown user", ErrNotFound)
		}
	}
	if _, err = tx.Exec(ctx, stmtClearMembers, listID); err != nil {
		return err
	}
	for _, userID := range userIDs {
		if _, err = tx.Exec(ctx, stmtAddMember, listID, userID); err != nil {
			return mapPg(err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListMembers(ctx context.Context, listID, afterUserID int64, limit int) ([]Membership, error) {
	if err := s.requireList(ctx, listID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, stmtListMembers, listID, afterUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMemberships(rows)
}

func (s *Store) ListUserMemberships(ctx context.Context, userID, afterListID int64, limit int) ([]Membership, error) {
	if err := s.requireUser(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, stmtListUserMemberships, userID, afterListID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMemberships(rows)
}

func (s *Store) requireList(ctx context.Context, id int64) error {
	var exists int64
	err := s.pool.QueryRow(ctx, stmtListExists, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) requireUser(ctx context.Context, id int64) error {
	var exists int64
	err := s.pool.QueryRow(ctx, stmtUserExists, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) getMembership(ctx context.Context, listID, userID int64) (Membership, error) {
	member, err := scanMembership(s.pool.QueryRow(ctx, stmtGetMembership, listID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	return member, err
}

func scanUser(row pgx.Row) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.Address, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt, &user.ListCount)
	return user, err
}

func scanList(row pgx.Row) (DistributionList, error) {
	var list DistributionList
	err := row.Scan(&list.ID, &list.Address, &list.DisplayName, &list.CreatedAt, &list.UpdatedAt, &list.MemberCount)
	return list, err
}

func scanMembership(row pgx.Row) (Membership, error) {
	var member Membership
	err := row.Scan(&member.ListID, &member.UserID, &member.ListAddress, &member.UserAddress, &member.UserDisplayName, &member.CreatedAt)
	return member, err
}

func collectUsers(rows pgx.Rows) ([]User, error) {
	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func collectLists(rows pgx.Rows) ([]DistributionList, error) {
	var lists []DistributionList
	for rows.Next() {
		list, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		lists = append(lists, list)
	}
	return lists, rows.Err()
}

func collectMemberships(rows pgx.Rows) ([]Membership, error) {
	var members []Membership
	for rows.Next() {
		member, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func mapPg(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505":
		return ErrExists
	case "23503":
		return ErrNotFound
	case "23514":
		return fmt.Errorf("%w: %s", ErrInvalid, pgErr.Message)
	default:
		return err
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
