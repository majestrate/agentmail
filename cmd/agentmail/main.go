// Command agentmail is the management client for the directory gRPC service.
// The client speaks plaintext. Istio sidecars authenticate the connection
// with mutual TLS when the command runs inside the mesh.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	directoryv1 "agentmail/gen/agentmail/directory/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const usageText = `agentmail is the directory management client.

The directory process does not terminate TLS. Run this command inside the
directory pod so its Istio sidecar can authenticate the call. Dial the
Service DNS. Localhost skips the sidecar.

  agentmail [-server host:port] <resource> <action> [flags]

Resources:
  users         mailbox users
  lists         distribution lists
  members       membership of one list
  memberships   lists that include one user

Actions:
  users create|get|ls|update|delete
  lists create|get|ls|update|delete
  members add|remove|set|ls
  memberships ls

Environment:
  AGENTMAIL_SERVER   directory address
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "agentmail: %s\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	server := getenv("AGENTMAIL_SERVER", "agentmail-directory.agentmail.svc.cluster.local:50051")
	if len(args) >= 2 && (args[0] == "-server" || args[0] == "--server") {
		server = args[1]
		args = args[2:]
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, usageText)
		if len(args) == 0 {
			return errors.New("resource is required")
		}
		return nil
	}
	if len(args) < 2 {
		return errors.New("action is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(server, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := directoryv1.NewDirectoryClient(conn)
	switch args[0] {
	case "users":
		return users(ctx, out, client, args[1], args[2:])
	case "lists":
		return lists(ctx, out, client, args[1], args[2:])
	case "members":
		return members(ctx, out, client, args[1], args[2:])
	case "memberships":
		return memberships(ctx, out, client, args[1], args[2:])
	default:
		return fmt.Errorf("unknown resource %q", args[0])
	}
}

func users(ctx context.Context, out io.Writer, client directoryv1.DirectoryClient, action string, args []string) error {
	switch action {
	case "create":
		fs := flag.NewFlagSet("users create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		address := fs.String("address", "", "mailbox address")
		display := fs.String("display-name", "", "display name")
		password := fs.String("password", "", "mailbox password")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *address == "" || *password == "" {
			return errors.New("users create requires -address and -password")
		}
		user, err := client.CreateUser(ctx, &directoryv1.CreateUserRequest{
			Address:     *address,
			DisplayName: *display,
			Password:    *password,
		})
		if err != nil {
			return err
		}
		printUser(out, user)
		return nil
	case "get":
		fs := flag.NewFlagSet("users get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.Int64("id", 0, "user id")
		address := fs.String("address", "", "mailbox address")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *id == 0 && *address == "" {
			return errors.New("users get requires -id or -address")
		}
		user, err := client.GetUser(ctx, &directoryv1.GetUserRequest{Id: *id, Address: *address})
		if err != nil {
			return err
		}
		printUser(out, user)
		return nil
	case "ls":
		fs := flag.NewFlagSet("users ls", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		pageSize := fs.Int("page-size", 100, "page size")
		if err := fs.Parse(args); err != nil {
			return err
		}
		token := ""
		for {
			resp, err := client.ListUsers(ctx, &directoryv1.ListUsersRequest{
				PageSize:  int32(*pageSize),
				PageToken: token,
			})
			if err != nil {
				return err
			}
			for _, user := range resp.GetUsers() {
				printUser(out, user)
			}
			token = resp.GetNextPageToken()
			if token == "" {
				return nil
			}
		}
	case "update":
		fs := flag.NewFlagSet("users update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.Int64("id", 0, "user id")
		address := newSetFlag()
		display := newSetFlag()
		password := newSetFlag()
		fs.Var(address, "address", "mailbox address")
		fs.Var(display, "display-name", "display name")
		fs.Var(password, "password", "mailbox password")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *id == 0 {
			return errors.New("users update requires -id")
		}
		req := &directoryv1.UpdateUserRequest{Id: *id}
		if address.set {
			req.Address = &address.value
		}
		if display.set {
			req.DisplayName = &display.value
		}
		if password.set {
			req.Password = &password.value
		}
		user, err := client.UpdateUser(ctx, req)
		if err != nil {
			return err
		}
		printUser(out, user)
		return nil
	case "delete":
		fs := flag.NewFlagSet("users delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.Int64("id", 0, "user id")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *id == 0 {
			return errors.New("users delete requires -id")
		}
		if _, err := client.DeleteUser(ctx, &directoryv1.DeleteUserRequest{Id: *id}); err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted user id=%d\n", *id)
		return nil
	default:
		return fmt.Errorf("unknown users action %q", action)
	}
}

func lists(ctx context.Context, out io.Writer, client directoryv1.DirectoryClient, action string, args []string) error {
	switch action {
	case "create":
		fs := flag.NewFlagSet("lists create", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		address := fs.String("address", "", "list address")
		display := fs.String("display-name", "", "display name")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *address == "" {
			return errors.New("lists create requires -address")
		}
		list, err := client.CreateDistributionList(ctx, &directoryv1.CreateDistributionListRequest{
			Address:     *address,
			DisplayName: *display,
		})
		if err != nil {
			return err
		}
		printList(out, list)
		return nil
	case "get":
		fs := flag.NewFlagSet("lists get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.Int64("id", 0, "list id")
		address := fs.String("address", "", "list address")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *id == 0 && *address == "" {
			return errors.New("lists get requires -id or -address")
		}
		list, err := client.GetDistributionList(ctx, &directoryv1.GetDistributionListRequest{Id: *id, Address: *address})
		if err != nil {
			return err
		}
		printList(out, list)
		return nil
	case "ls":
		fs := flag.NewFlagSet("lists ls", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		pageSize := fs.Int("page-size", 100, "page size")
		if err := fs.Parse(args); err != nil {
			return err
		}
		token := ""
		for {
			resp, err := client.ListDistributionLists(ctx, &directoryv1.ListDistributionListsRequest{
				PageSize:  int32(*pageSize),
				PageToken: token,
			})
			if err != nil {
				return err
			}
			for _, list := range resp.GetDistributionLists() {
				printList(out, list)
			}
			token = resp.GetNextPageToken()
			if token == "" {
				return nil
			}
		}
	case "update":
		fs := flag.NewFlagSet("lists update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.Int64("id", 0, "list id")
		address := newSetFlag()
		display := newSetFlag()
		fs.Var(address, "address", "list address")
		fs.Var(display, "display-name", "display name")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *id == 0 {
			return errors.New("lists update requires -id")
		}
		req := &directoryv1.UpdateDistributionListRequest{Id: *id}
		if address.set {
			req.Address = &address.value
		}
		if display.set {
			req.DisplayName = &display.value
		}
		list, err := client.UpdateDistributionList(ctx, req)
		if err != nil {
			return err
		}
		printList(out, list)
		return nil
	case "delete":
		fs := flag.NewFlagSet("lists delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.Int64("id", 0, "list id")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *id == 0 {
			return errors.New("lists delete requires -id")
		}
		if _, err := client.DeleteDistributionList(ctx, &directoryv1.DeleteDistributionListRequest{Id: *id}); err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted list id=%d\n", *id)
		return nil
	default:
		return fmt.Errorf("unknown lists action %q", action)
	}
}

func members(ctx context.Context, out io.Writer, client directoryv1.DirectoryClient, action string, args []string) error {
	switch action {
	case "add":
		listID, userID, err := memberPair("members add", args)
		if err != nil {
			return err
		}
		member, err := client.AddMember(ctx, &directoryv1.AddMemberRequest{ListId: listID, UserId: userID})
		if err != nil {
			return err
		}
		printMember(out, member)
		return nil
	case "remove":
		listID, userID, err := memberPair("members remove", args)
		if err != nil {
			return err
		}
		if _, err := client.RemoveMember(ctx, &directoryv1.RemoveMemberRequest{ListId: listID, UserId: userID}); err != nil {
			return err
		}
		fmt.Fprintf(out, "removed list=%d user=%d\n", listID, userID)
		return nil
	case "set":
		fs := flag.NewFlagSet("members set", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		listID := fs.Int64("list", 0, "list id")
		users := fs.String("users", "", "comma-separated user ids")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *listID == 0 {
			return errors.New("members set requires -list")
		}
		ids, err := parseIDs(*users)
		if err != nil {
			return err
		}
		resp, err := client.SetMembers(ctx, &directoryv1.SetMembersRequest{ListId: *listID, UserIds: ids})
		if err != nil {
			return err
		}
		for _, member := range resp.GetMembers() {
			printMember(out, member)
		}
		return nil
	case "ls":
		fs := flag.NewFlagSet("members ls", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		listID := fs.Int64("list", 0, "list id")
		pageSize := fs.Int("page-size", 100, "page size")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *listID == 0 {
			return errors.New("members ls requires -list")
		}
		token := ""
		for {
			resp, err := client.ListMembers(ctx, &directoryv1.ListMembersRequest{
				ListId:    *listID,
				PageSize:  int32(*pageSize),
				PageToken: token,
			})
			if err != nil {
				return err
			}
			for _, member := range resp.GetMembers() {
				printMember(out, member)
			}
			token = resp.GetNextPageToken()
			if token == "" {
				return nil
			}
		}
	default:
		return fmt.Errorf("unknown members action %q", action)
	}
}

func memberships(ctx context.Context, out io.Writer, client directoryv1.DirectoryClient, action string, args []string) error {
	if action != "ls" {
		return fmt.Errorf("unknown memberships action %q", action)
	}
	fs := flag.NewFlagSet("memberships ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	userID := fs.Int64("user", 0, "user id")
	pageSize := fs.Int("page-size", 100, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *userID == 0 {
		return errors.New("memberships ls requires -user")
	}
	token := ""
	for {
		resp, err := client.ListUserMemberships(ctx, &directoryv1.ListUserMembershipsRequest{
			UserId:    *userID,
			PageSize:  int32(*pageSize),
			PageToken: token,
		})
		if err != nil {
			return err
		}
		for _, member := range resp.GetMemberships() {
			printMember(out, member)
		}
		token = resp.GetNextPageToken()
		if token == "" {
			return nil
		}
	}
}

func memberPair(name string, args []string) (int64, int64, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listID := fs.Int64("list", 0, "list id")
	userID := fs.Int64("user", 0, "user id")
	if err := fs.Parse(args); err != nil {
		return 0, 0, err
	}
	if *listID == 0 || *userID == 0 {
		return 0, 0, fmt.Errorf("%s requires -list and -user", name)
	}
	return *listID, *userID, nil
}

func parseIDs(text string) ([]int64, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	parts := strings.Split(text, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid user id %q", part)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func printUser(out io.Writer, user *directoryv1.User) {
	fmt.Fprintf(out, "id=%d address=%s display_name=%s lists=%d created=%s updated=%s\n",
		user.GetId(), user.GetAddress(), user.GetDisplayName(), user.GetListCount(), stamp(user.GetCreatedAt()), stamp(user.GetUpdatedAt()))
}

func printList(out io.Writer, list *directoryv1.DistributionList) {
	fmt.Fprintf(out, "id=%d address=%s display_name=%s members=%d created=%s updated=%s\n",
		list.GetId(), list.GetAddress(), list.GetDisplayName(), list.GetMemberCount(), stamp(list.GetCreatedAt()), stamp(list.GetUpdatedAt()))
}

func printMember(out io.Writer, member *directoryv1.Membership) {
	fmt.Fprintf(out, "list=%d user=%d list_address=%s user_address=%s display_name=%s created=%s\n",
		member.GetListId(), member.GetUserId(), member.GetListAddress(), member.GetUserAddress(), member.GetUserDisplayName(), stamp(member.GetCreatedAt()))
}

func stamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

type setFlag struct {
	set   bool
	value string
}

func newSetFlag() *setFlag { return &setFlag{} }

func (s *setFlag) String() string { return s.value }

func (s *setFlag) Set(value string) error {
	s.set = true
	s.value = value
	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
