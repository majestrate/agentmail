package directory

import (
	"context"
	"errors"
	"log"

	directoryv1 "agentmail/gen/agentmail/directory/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type userDeleter interface {
	GetUser(ctx context.Context, id int64, address string) (User, error)
	DeleteUser(ctx context.Context, id int64) error
}

type Server struct {
	directoryv1.UnimplementedDirectoryServer
	store   *Store
	users   userDeleter
	mail    Maildir
	inboxes InboxRemover
}

func NewServer(store *Store, mail Maildir, inboxes InboxRemover) *Server {
	return &Server{store: store, users: store, mail: mail, inboxes: inboxes}
}

func (s *Server) CreateUser(ctx context.Context, req *directoryv1.CreateUserRequest) (*directoryv1.User, error) {
	address, displayName, err := identity(req.GetAddress(), req.GetDisplayName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	hash, err := HashPassword(req.GetPassword())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	user, err := s.store.CreateUser(ctx, address, displayName, hash)
	if err != nil {
		return nil, rpcErr(err)
	}
	if err := s.mail.CreateInbox(user.Address); err != nil {
		if delErr := s.store.DeleteUser(ctx, user.ID); delErr != nil {
			log.Printf("directory: remove user %d after mailbox failure: %v", user.ID, delErr)
		}
		log.Printf("directory: mailbox: %v", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	return userProto(user), nil
}

func (s *Server) GetUser(ctx context.Context, req *directoryv1.GetUserRequest) (*directoryv1.User, error) {
	id, address, err := lookup(req.GetId(), req.GetAddress())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	user, err := s.store.GetUser(ctx, id, address)
	if err != nil {
		return nil, rpcErr(err)
	}
	return userProto(user), nil
}

func (s *Server) ListUsers(ctx context.Context, req *directoryv1.ListUsersRequest) (*directoryv1.ListUsersResponse, error) {
	after, limit, err := page(req.GetPageToken(), req.GetPageSize())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	users, err := s.store.ListUsers(ctx, after, limit)
	if err != nil {
		return nil, rpcErr(err)
	}
	resp := &directoryv1.ListUsersResponse{}
	for _, user := range users {
		resp.Users = append(resp.Users, userProto(user))
	}
	if len(users) == limit {
		resp.NextPageToken = EncodePageToken(users[len(users)-1].ID)
	}
	return resp, nil
}

func (s *Server) UpdateUser(ctx context.Context, req *directoryv1.UpdateUserRequest) (*directoryv1.User, error) {
	if req.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	var address, displayName, hash *string
	if req.Address != nil {
		value, err := CanonicalAddress(req.GetAddress())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		address = &value
	}
	if req.DisplayName != nil {
		value, err := CleanDisplayName(req.GetDisplayName())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		displayName = &value
	}
	if req.Password != nil {
		value, err := HashPassword(req.GetPassword())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		hash = &value
	}
	user, err := s.store.UpdateUser(ctx, req.GetId(), address, displayName, hash)
	if err != nil {
		return nil, rpcErr(err)
	}
	return userProto(user), nil
}

func (s *Server) DeleteUser(ctx context.Context, req *directoryv1.DeleteUserRequest) (*directoryv1.DeleteUserResponse, error) {
	if req.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if s.inboxes == nil {
		log.Printf("directory: maildir service is not configured")
		return nil, status.Error(codes.Internal, "internal error")
	}
	user, err := s.users.GetUser(ctx, req.GetId(), "")
	if err != nil {
		return nil, rpcErr(err)
	}
	if err := s.inboxes.RemoveInbox(ctx, user.Address); err != nil {
		log.Printf("directory: remove inbox for user %d: %v", user.ID, err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	if err := s.users.DeleteUser(ctx, user.ID); err != nil {
		if recErr := s.mail.CreateInbox(user.Address); recErr != nil {
			log.Printf("directory: restore inbox for user %d after delete failure: %v", user.ID, recErr)
		}
		return nil, rpcErr(err)
	}
	return &directoryv1.DeleteUserResponse{}, nil
}

func (s *Server) CreateDistributionList(ctx context.Context, req *directoryv1.CreateDistributionListRequest) (*directoryv1.DistributionList, error) {
	address, displayName, err := identity(req.GetAddress(), req.GetDisplayName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	list, err := s.store.CreateDistributionList(ctx, address, displayName)
	if err != nil {
		return nil, rpcErr(err)
	}
	return listProto(list), nil
}

func (s *Server) GetDistributionList(ctx context.Context, req *directoryv1.GetDistributionListRequest) (*directoryv1.DistributionList, error) {
	id, address, err := lookup(req.GetId(), req.GetAddress())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	list, err := s.store.GetDistributionList(ctx, id, address)
	if err != nil {
		return nil, rpcErr(err)
	}
	return listProto(list), nil
}

func (s *Server) ListDistributionLists(ctx context.Context, req *directoryv1.ListDistributionListsRequest) (*directoryv1.ListDistributionListsResponse, error) {
	after, limit, err := page(req.GetPageToken(), req.GetPageSize())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	lists, err := s.store.ListDistributionLists(ctx, after, limit)
	if err != nil {
		return nil, rpcErr(err)
	}
	resp := &directoryv1.ListDistributionListsResponse{}
	for _, list := range lists {
		resp.DistributionLists = append(resp.DistributionLists, listProto(list))
	}
	if len(lists) == limit {
		resp.NextPageToken = EncodePageToken(lists[len(lists)-1].ID)
	}
	return resp, nil
}

func (s *Server) UpdateDistributionList(ctx context.Context, req *directoryv1.UpdateDistributionListRequest) (*directoryv1.DistributionList, error) {
	if req.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	var address, displayName *string
	if req.Address != nil {
		value, err := CanonicalAddress(req.GetAddress())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		address = &value
	}
	if req.DisplayName != nil {
		value, err := CleanDisplayName(req.GetDisplayName())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		displayName = &value
	}
	list, err := s.store.UpdateDistributionList(ctx, req.GetId(), address, displayName)
	if err != nil {
		return nil, rpcErr(err)
	}
	return listProto(list), nil
}

func (s *Server) DeleteDistributionList(ctx context.Context, req *directoryv1.DeleteDistributionListRequest) (*directoryv1.DeleteDistributionListResponse, error) {
	if req.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	return &directoryv1.DeleteDistributionListResponse{}, rpcErr(s.store.DeleteDistributionList(ctx, req.GetId()))
}

func (s *Server) AddMember(ctx context.Context, req *directoryv1.AddMemberRequest) (*directoryv1.Membership, error) {
	if req.GetListId() <= 0 || req.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "list_id and user_id are required")
	}
	member, err := s.store.AddMember(ctx, req.GetListId(), req.GetUserId())
	if err != nil {
		return nil, rpcErr(err)
	}
	return membershipProto(member), nil
}

func (s *Server) RemoveMember(ctx context.Context, req *directoryv1.RemoveMemberRequest) (*directoryv1.RemoveMemberResponse, error) {
	if req.GetListId() <= 0 || req.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "list_id and user_id are required")
	}
	return &directoryv1.RemoveMemberResponse{}, rpcErr(s.store.RemoveMember(ctx, req.GetListId(), req.GetUserId()))
}

func (s *Server) SetMembers(ctx context.Context, req *directoryv1.SetMembersRequest) (*directoryv1.ListMembersResponse, error) {
	if req.GetListId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "list_id is required")
	}
	seen := make(map[int64]struct{}, len(req.GetUserIds()))
	ids := make([]int64, 0, len(req.GetUserIds()))
	for _, id := range req.GetUserIds() {
		if id <= 0 {
			return nil, status.Error(codes.InvalidArgument, "user_ids must be positive")
		}
		if _, ok := seen[id]; ok {
			return nil, status.Error(codes.InvalidArgument, "user_ids contains a duplicate")
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if err := s.store.SetMembers(ctx, req.GetListId(), ids); err != nil {
		return nil, rpcErr(err)
	}
	members, err := s.store.ListMembers(ctx, req.GetListId(), 0, max(len(ids), 1))
	if err != nil {
		return nil, rpcErr(err)
	}
	resp := &directoryv1.ListMembersResponse{}
	for _, member := range members {
		resp.Members = append(resp.Members, membershipProto(member))
	}
	return resp, nil
}

func (s *Server) ListMembers(ctx context.Context, req *directoryv1.ListMembersRequest) (*directoryv1.ListMembersResponse, error) {
	if req.GetListId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "list_id is required")
	}
	after, limit, err := page(req.GetPageToken(), req.GetPageSize())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	members, err := s.store.ListMembers(ctx, req.GetListId(), after, limit)
	if err != nil {
		return nil, rpcErr(err)
	}
	resp := &directoryv1.ListMembersResponse{}
	for _, member := range members {
		resp.Members = append(resp.Members, membershipProto(member))
	}
	if len(members) == limit {
		resp.NextPageToken = EncodePageToken(members[len(members)-1].UserID)
	}
	return resp, nil
}

func (s *Server) ListUserMemberships(ctx context.Context, req *directoryv1.ListUserMembershipsRequest) (*directoryv1.ListUserMembershipsResponse, error) {
	if req.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	after, limit, err := page(req.GetPageToken(), req.GetPageSize())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	members, err := s.store.ListUserMemberships(ctx, req.GetUserId(), after, limit)
	if err != nil {
		return nil, rpcErr(err)
	}
	resp := &directoryv1.ListUserMembershipsResponse{}
	for _, member := range members {
		resp.Memberships = append(resp.Memberships, membershipProto(member))
	}
	if len(members) == limit {
		resp.NextPageToken = EncodePageToken(members[len(members)-1].ListID)
	}
	return resp, nil
}

func identity(address, displayName string) (string, string, error) {
	canonical, err := CanonicalAddress(address)
	if err != nil {
		return "", "", err
	}
	name, err := CleanDisplayName(displayName)
	if err != nil {
		return "", "", err
	}
	return canonical, name, nil
}

func lookup(id int64, address string) (int64, string, error) {
	switch {
	case id > 0 && address == "":
		return id, "", nil
	case id == 0 && address != "":
		canonical, err := CanonicalAddress(address)
		return 0, canonical, err
	default:
		return 0, "", errors.New("set exactly one of id or address")
	}
}

func page(token string, size int32) (int64, int, error) {
	after, err := DecodePageToken(token)
	if err != nil {
		return 0, 0, err
	}
	switch {
	case size <= 0:
		return after, 100, nil
	case size > 500:
		return after, 500, nil
	default:
		return after, int(size), nil
	}
}

func rpcErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		log.Printf("directory: %v", err)
		return status.Error(codes.Internal, "internal error")
	}
}

func userProto(user User) *directoryv1.User {
	return &directoryv1.User{
		Id:          user.ID,
		Address:     user.Address,
		DisplayName: user.DisplayName,
		CreatedAt:   timestamppb.New(user.CreatedAt),
		UpdatedAt:   timestamppb.New(user.UpdatedAt),
		ListCount:   user.ListCount,
	}
}

func listProto(list DistributionList) *directoryv1.DistributionList {
	return &directoryv1.DistributionList{
		Id:          list.ID,
		Address:     list.Address,
		DisplayName: list.DisplayName,
		CreatedAt:   timestamppb.New(list.CreatedAt),
		UpdatedAt:   timestamppb.New(list.UpdatedAt),
		MemberCount: list.MemberCount,
	}
}

func membershipProto(member Membership) *directoryv1.Membership {
	return &directoryv1.Membership{
		ListId:          member.ListID,
		UserId:          member.UserID,
		ListAddress:     member.ListAddress,
		UserAddress:     member.UserAddress,
		UserDisplayName: member.UserDisplayName,
		CreatedAt:       timestamppb.New(member.CreatedAt),
	}
}
