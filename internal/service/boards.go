package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// maxBoardName is the longest board name Planka stores, in UTF-16 units.
const maxBoardName = 128

// MemberRoles are the roles a board member can have. A viewer may also be
// allowed to comment.
var MemberRoles = []string{"editor", "viewer"}

// Boards creates, renames and deletes boards, and manages who may use them.
// Reading a board is (*Catalog).GetBoard. Planka lets only a project manager
// do any of this, and only an admin or project owner add other people.
type Boards struct {
	cards *Cards
	lists ListsAPI
	api   BoardWriteAPI
}

// NewBoards returns a Boards service. It finds boards and holds their write
// locks through cards, creates a new board's lists through lists, and writes
// boards and memberships through api.
func NewBoards(cards *Cards, lists ListsAPI, api BoardWriteAPI) *Boards {
	return &Boards{cards: cards, lists: lists, api: api}
}

// CreateBoard describes a new board in Project (ID or name): its active
// Lists in order, then ClosedList, a closed done-list, when given.
type CreateBoard struct {
	Project, Name string
	Lists         []string
	ClosedList    string
}

// DeletedBoard is a deleted board and how many cards went with it. AtLeast
// says the archive held more than one page of cards, which Planka 2.1.1
// cannot page through, so Cards is a lower bound.
type DeletedBoard struct {
	ID, Name, Project string
	Cards             int
	AtLeast           bool
}

// Member is a person on a board. ID is the membership's ID.
type Member struct {
	ID, UserID, Name, Username, Role string
	CanComment                       bool
}

// AddMember gives Person (username, full name, email, or "me") access to
// Board with Role, one of MemberRoles. CanComment applies to viewers.
type AddMember struct {
	Board, Person, Role string
	CanComment          *bool
}

// UpdateMember changes the role of Person, a member of Board, or whether a
// viewer may comment. A nil field is left alone.
type UpdateMember struct {
	Board, Person string
	Role          *string
	CanComment    *bool
}

// Create adds a board at the end of a project, then its lists, and returns
// the board as GetBoard shows it.
func (s *Boards) Create(ctx context.Context, in CreateBoard) (*Board, error) {
	name, err := boardName(in.Name)
	if err != nil {
		return nil, err
	}
	lists, err := newBoardLists(in.Lists, in.ClosedList)
	if err != nil {
		return nil, err
	}
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	project, err := pickProject(resp, in.Project)
	if err != nil {
		return nil, err
	}
	var positions []float64
	for _, b := range resp.Included.Boards {
		if b.ProjectID != project.ID {
			continue
		}
		if normalize(b.Name) == normalize(name) {
			return nil, fmt.Errorf("project %q already has a board %q (id %s): pick another name", project.Name, b.Name, b.ID)
		}
		positions = append(positions, b.Position)
	}

	created, err := s.api.CreateBoard(ctx, project.ID, name, bottomPosition(positions))
	if err != nil {
		return nil, managerOnly(err, fmt.Sprintf("create a board in project %q", project.Name))
	}
	for i, l := range lists {
		req := planka.CreateListRequest{Type: l.kind, Position: float64(i+1) * positionGap, Name: l.name}
		if _, err := s.lists.CreateList(ctx, created.ID, req); err != nil {
			return nil, fmt.Errorf("board %q (id %s) was created, but creating its list %q failed: %w", name, created.ID, l.name, err)
		}
	}
	board, err := s.cards.boards.Board(ctx, created.ID)
	if err != nil {
		return nil, fmt.Errorf("board %q (id %s) was created, but reading it back failed: %w", name, created.ID, err)
	}
	return newSnapshot(board, project.Name).board(), nil
}

// Rename gives a board a new name, which must differ from the other boards
// of its project.
func (s *Boards) Rename(ctx context.Context, board, name string) (*BoardRef, error) {
	name, err := boardName(name)
	if err != nil {
		return nil, err
	}
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var refs []BoardRef
	for _, g := range groupBoards(resp) {
		refs = append(refs, g.Boards...)
	}
	br, err := pickBoard(refs, board)
	if err != nil {
		return nil, err
	}
	for _, other := range refs {
		if other.ID != br.ID && other.Project == br.Project && normalize(other.Name) == normalize(name) {
			return nil, fmt.Errorf("project %q already has a board %q (id %s): pick another name", br.Project, other.Name, other.ID)
		}
	}
	if br.Name == name {
		return &br, nil
	}
	updated, err := s.api.UpdateBoard(ctx, br.ID, name)
	if err != nil {
		return nil, managerOnly(err, fmt.Sprintf("rename board %q", br.Name))
	}
	return &BoardRef{ID: updated.ID, Name: updated.Name, Project: br.Project}, nil
}

// Delete deletes a board with its lists and cards. A board that still has
// cards, archived ones included, is refused unless withCards is set.
func (s *Boards) Delete(ctx context.Context, board string, withCards bool) (*DeletedBoard, error) {
	snap, unlock, err := s.cards.lockBoardRef(ctx, board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	out := &DeletedBoard{ID: snap.resp.Item.ID, Name: snap.resp.Item.Name, Project: snap.project, Cards: len(snap.resp.Included.Cards)}
	if arch := snap.archiveList(); arch != nil {
		// One page: Planka 2.1.1 fails the next ones (see labelCards).
		page, err := s.cards.cards.ListCards(ctx, arch.ID, planka.CardQuery{})
		if err != nil {
			return nil, err
		}
		out.Cards += len(page.Items)
		out.AtLeast = len(page.Items) >= plankaCardsPage
	}
	if out.Cards > 0 && !withCards {
		count := fmt.Sprint(out.Cards)
		if out.AtLeast {
			count += " or more"
		}
		return nil, fmt.Errorf("board %q has %s cards, archived ones included, and deleting it deletes them for good: "+
			"set with_cards=true to delete it anyway", out.Name, count)
	}
	if err := s.api.DeleteBoard(ctx, out.ID); err != nil {
		return nil, managerOnly(err, fmt.Sprintf("delete board %q", out.Name))
	}
	return out, nil
}

// AddMember gives a person access to a board.
func (s *Boards) AddMember(ctx context.Context, in AddMember) (*Member, error) {
	if err := checkMemberRole(in.Role, in.CanComment); err != nil {
		return nil, err
	}
	snap, unlock, err := s.cards.lockBoardRef(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	user, err := s.findPerson(ctx, snap, in.Person)
	if err != nil {
		return nil, err
	}
	if m, ok := membership(snap, user.ID); ok {
		return nil, fmt.Errorf("%s is already on %s as %s: use update_board_member to change that", userName(user), snap.scope(), m.Role)
	}
	req := planka.BoardMembershipRequest{UserID: planka.Some(user.ID), Role: planka.Some(in.Role)}
	if in.CanComment != nil {
		req.CanComment = planka.Some(*in.CanComment)
	}
	created, err := s.api.CreateBoardMembership(ctx, snap.resp.Item.ID, req)
	if err != nil {
		return nil, memberRights(err, fmt.Sprintf("add people to %s", snap.scope()))
	}
	out := toMember(*created, user)
	return &out, nil
}

// UpdateMember changes a member's role, or whether a viewer may comment.
func (s *Boards) UpdateMember(ctx context.Context, in UpdateMember) (*Member, error) {
	if in.Role == nil && in.CanComment == nil {
		return nil, errors.New("nothing to change: give role or can_comment")
	}
	snap, unlock, err := s.cards.lockBoardRef(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, user, err := s.findMember(ctx, snap, in.Person)
	if err != nil {
		return nil, err
	}
	role := m.Role
	if in.Role != nil {
		role = *in.Role
	}
	if err := checkMemberRole(role, in.CanComment); err != nil {
		return nil, err
	}
	var req planka.BoardMembershipRequest
	if role != m.Role {
		req.Role = planka.Some(role)
	}
	if in.CanComment != nil && *in.CanComment != (m.CanComment != nil && *m.CanComment) {
		req.CanComment = planka.Some(*in.CanComment)
	}
	if req == (planka.BoardMembershipRequest{}) {
		out := toMember(m, user)
		return &out, nil
	}
	updated, err := s.api.UpdateBoardMembership(ctx, m.ID, req)
	if err != nil {
		return nil, managerOnly(err, fmt.Sprintf("change members of %s", snap.scope()))
	}
	out := toMember(*updated, user)
	return &out, nil
}

// RemoveMember takes a person's access to a board away, and returns the
// membership as it was.
func (s *Boards) RemoveMember(ctx context.Context, board, person string) (*Member, error) {
	snap, unlock, err := s.cards.lockBoardRef(ctx, board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, user, err := s.findMember(ctx, snap, person)
	if err != nil {
		return nil, err
	}
	if err := s.api.DeleteBoardMembership(ctx, m.ID); err != nil {
		return nil, managerOnly(err, fmt.Sprintf("remove members of %s", snap.scope()))
	}
	out := toMember(m, user)
	return &out, nil
}

// findPerson finds the user a person names: "me", a board member, or else
// any Planka user by username, email or full name. Listing every user is
// for admins and project owners only, as is adding someone else.
func (s *Boards) findPerson(ctx context.Context, snap *boardSnapshot, person string) (planka.User, error) {
	person = strings.TrimSpace(person)
	if person == "" {
		return planka.User{}, errors.New("person is empty: give a username, full name, email or \"me\"")
	}
	if normalize(person) == "me" {
		me, err := s.cards.users.Me(ctx)
		if err != nil {
			return planka.User{}, err
		}
		return *me, nil
	}
	if u, ok := exactUser(snap.resp.Included.Users, person); ok {
		return u, nil
	}
	users, err := s.api.Users(ctx)
	if err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusForbidden {
			return planka.User{}, errors.New("only a Planka admin or project owner can add other people to a board; " +
				"a project manager can add themselves with person=me")
		}
		return planka.User{}, err
	}
	if u, ok := exactUser(users, person); ok {
		return u, nil
	}
	return matchName("user", person, "Planka", users, userName, userID)
}

// exactUser finds the user whose username, email or full name is person,
// case ignored.
func exactUser(users []planka.User, person string) (planka.User, bool) {
	q := normalize(person)
	i := slices.IndexFunc(users, func(u planka.User) bool {
		return normalize(u.Username) == q || (u.Email != "" && normalize(u.Email) == q) || (u.Name != "" && normalize(u.Name) == q)
	})
	if i < 0 {
		return planka.User{}, false
	}
	return users[i], true
}

// findMember finds a board member by "me", username or full name, with the
// membership.
func (s *Boards) findMember(ctx context.Context, snap *boardSnapshot, person string) (planka.BoardMembership, planka.User, error) {
	meID := ""
	if normalize(person) == "me" {
		me, err := s.cards.users.Me(ctx)
		if err != nil {
			return planka.BoardMembership{}, planka.User{}, err
		}
		meID = me.ID
	}
	members := snap.members()
	u, err := members.userByName(person, meID)
	if err != nil {
		return planka.BoardMembership{}, planka.User{}, err
	}
	m, ok := membership(snap, u.ID)
	if !ok {
		return planka.BoardMembership{}, planka.User{}, fmt.Errorf("%s is not a member of %s", userName(u), snap.scope())
	}
	if _, known := snap.users[u.ID]; known {
		u = snap.users[u.ID]
	}
	return m, u, nil
}

// membership is the user's membership of the board, if any.
func membership(snap *boardSnapshot, userID string) (planka.BoardMembership, bool) {
	i := slices.IndexFunc(snap.resp.Included.BoardMemberships, func(m planka.BoardMembership) bool { return m.UserID == userID })
	if i < 0 {
		return planka.BoardMembership{}, false
	}
	return snap.resp.Included.BoardMemberships[i], true
}

func toMember(m planka.BoardMembership, u planka.User) Member {
	return Member{
		ID: m.ID, UserID: m.UserID, Name: userName(u), Username: u.Username, Role: m.Role,
		CanComment: m.Role == "editor" || (m.CanComment != nil && *m.CanComment),
	}
}

// checkMemberRole checks a role, and that can_comment is not set for an
// editor, who can always comment.
func checkMemberRole(role string, canComment *bool) error {
	switch {
	case !slices.Contains(MemberRoles, role):
		return fmt.Errorf("unknown role %q: use editor or viewer", role)
	case role == "editor" && canComment != nil:
		return errors.New("can_comment is for viewers: an editor can always comment")
	}
	return nil
}

// boardName trims a board name and checks it is not empty or too long.
func boardName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("board name is empty")
	case jsLen(name) > maxBoardName:
		return "", fmt.Errorf("board name is longer than %d characters", maxBoardName)
	}
	return name, nil
}

// boardList is a list create_board makes: its name and Planka type.
type boardList struct{ name, kind string }

// newBoardLists checks a new board's list names, which must differ from each
// other, case ignored.
func newBoardLists(active []string, closed string) ([]boardList, error) {
	var out []boardList
	add := func(name, kind string) error {
		name, err := listName(name)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(out, func(l boardList) bool { return normalize(l.name) == normalize(name) }) {
			return fmt.Errorf("list %q is given twice", name)
		}
		out = append(out, boardList{name, kind})
		return nil
	}
	for _, name := range active {
		if err := add(name, "active"); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(closed) != "" {
		if err := add(closed, "closed"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// pickProject finds a project by ID or name among those the user can see.
func pickProject(resp *planka.ProjectsResponse, ref string) (planka.Project, error) {
	if strings.TrimSpace(ref) == "" {
		return planka.Project{}, errors.New("project is empty: give its name or ID")
	}
	if p, ok := byID(ref, resp.Items, func(p planka.Project) string { return p.ID }); ok {
		return p, nil
	}
	return matchName("project", strings.TrimSpace(ref), "", resp.Items,
		func(p planka.Project) string { return p.Name }, func(p planka.Project) string { return p.ID })
}

// managerOnly explains Planka refusing a write only a project manager may
// make: it answers 403, or 404 to hide what the user may not touch.
func managerOnly(err error, what string) error {
	var api *planka.APIError
	if errors.As(err, &api) && (api.Status == http.StatusForbidden || api.Status == http.StatusNotFound) {
		return fmt.Errorf("only a project manager of the project can %s, and Planka refused (HTTP %d)", what, api.Status)
	}
	return err
}

// memberRights is managerOnly for adding a member, where a 403 means the
// user is a project manager but may add only themselves.
func memberRights(err error, what string) error {
	var api *planka.APIError
	if errors.As(err, &api) && api.Status == http.StatusForbidden {
		return fmt.Errorf("only a Planka admin or project owner can %s other than themselves (HTTP 403); "+
			"a project manager can add themselves with person=me", what)
	}
	return managerOnly(err, what)
}
