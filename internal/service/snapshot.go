package service

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// archiveWord is the reserved list name that stands for the board's archive.
const archiveWord = "archive"

// boardSnapshot is a board as Planka returned it, indexed for the lookups the
// services need: list, label and user by name, and a card's short summary.
// Archived cards are not part of it.
type boardSnapshot struct {
	resp      *planka.BoardResponse
	project   string
	labels    map[string]planka.Label
	users     map[string]planka.User
	cards     map[string]planka.Card
	lists     map[string]planka.List
	cardLabel map[string][]string // card ID -> label IDs
	cardUser  map[string][]string // card ID -> user IDs
	cardTasks map[string][]planka.Task
}

func newSnapshot(r *planka.BoardResponse, project string) *boardSnapshot {
	inc := r.Included
	s := &boardSnapshot{
		resp:      r,
		project:   project,
		labels:    make(map[string]planka.Label, len(inc.Labels)),
		users:     make(map[string]planka.User, len(inc.Users)),
		cards:     make(map[string]planka.Card, len(inc.Cards)),
		lists:     make(map[string]planka.List, len(inc.Lists)),
		cardLabel: map[string][]string{},
		cardUser:  map[string][]string{},
		cardTasks: map[string][]planka.Task{},
	}
	for _, l := range inc.Labels {
		s.labels[l.ID] = l
	}
	for _, u := range inc.Users {
		s.users[u.ID] = u
	}
	for _, c := range inc.Cards {
		s.cards[c.ID] = c
	}
	for _, l := range inc.Lists {
		s.lists[l.ID] = l
	}
	for _, cl := range inc.CardLabels {
		s.cardLabel[cl.CardID] = append(s.cardLabel[cl.CardID], cl.LabelID)
	}
	for _, m := range inc.CardMemberships {
		s.cardUser[m.CardID] = append(s.cardUser[m.CardID], m.UserID)
	}
	taskListCard := make(map[string]string, len(inc.TaskLists))
	for _, tl := range inc.TaskLists {
		taskListCard[tl.ID] = tl.CardID
	}
	for _, t := range inc.Tasks {
		if cardID, ok := taskListCard[t.TaskListID]; ok {
			s.cardTasks[cardID] = append(s.cardTasks[cardID], t)
		}
	}
	return s
}

// scope names the board in error messages.
func (s *boardSnapshot) scope() string {
	return fmt.Sprintf("board %q", s.resp.Item.Name)
}

// kanbanLists returns the lists that show as columns: the active ones, then
// the closed ones, each by position. Archive and trash are left out.
func (s *boardSnapshot) kanbanLists() []planka.List {
	var out []planka.List
	for _, l := range s.resp.Included.Lists {
		if l.Type == "active" || l.Type == "closed" {
			out = append(out, l)
		}
	}
	slices.SortStableFunc(out, func(a, b planka.List) int {
		if c := cmp.Compare(listRank(a), listRank(b)); c != 0 {
			return c
		}
		return comparePosition(a.Position, b.Position)
	})
	return out
}

func listRank(l planka.List) int {
	if l.Type == "closed" {
		return 1
	}
	return 0
}

// comparePosition orders positions ascending, a missing one last.
func comparePosition(a, b *float64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	default:
		return cmp.Compare(*a, *b)
	}
}

// archiveList returns the board's archive list, or nil if it has none.
func (s *boardSnapshot) archiveList() *planka.List {
	for _, l := range s.resp.Included.Lists {
		if l.Type == archiveWord {
			return &l
		}
	}
	return nil
}

// kanbanListByName finds a kanban list by ID, or else by name; see matchName
// for the rules.
func (s *boardSnapshot) kanbanListByName(name string) (planka.List, error) {
	lists := s.kanbanLists()
	if l, ok := byID(name, lists, listID); ok {
		return l, nil
	}
	return matchName("list", name, s.scope(), lists, listLabel, listID)
}

// listByName finds a list by name. Besides the kanban lists it accepts the
// reserved word "archive" for the board's archive list, but only when no
// kanban list is called exactly that: a real column named "Archive" wins.
func (s *boardSnapshot) listByName(name string) (planka.List, error) {
	if normalize(name) == archiveWord && !slices.ContainsFunc(s.kanbanLists(), func(l planka.List) bool {
		return normalize(listLabel(l)) == archiveWord
	}) {
		if a := s.archiveList(); a != nil {
			return *a, nil
		}
	}
	return s.kanbanListByName(name)
}

// labelByName finds a label by ID, or else by its display name.
func (s *boardSnapshot) labelByName(name string) (planka.Label, error) {
	labels := s.sortedLabels()
	if l, ok := byID(name, labels, labelID); ok {
		return l, nil
	}
	return matchName("label", name, s.scope(), labels, labelName, labelID)
}

// userByName finds a user by ID, username or full name, in that order. "me"
// is the user meID.
func (s *boardSnapshot) userByName(name, meID string) (planka.User, error) {
	q := normalize(name)
	if q == "me" {
		if u, ok := s.users[meID]; ok {
			return u, nil
		}
		return planka.User{ID: meID}, nil
	}
	users := s.resp.Included.Users
	if u, ok := byID(name, users, userID); ok {
		return u, nil
	}
	for _, u := range users {
		if q != "" && normalize(u.Username) == q {
			return u, nil
		}
	}
	return matchName("user", name, s.scope(), users, userName, userID)
}

func listID(l planka.List) string   { return l.ID }
func labelID(l planka.Label) string { return l.ID }
func userID(u planka.User) string   { return u.ID }

func (s *boardSnapshot) sortedLabels() []planka.Label {
	out := slices.Clone(s.resp.Included.Labels)
	slices.SortStableFunc(out, func(a, b planka.Label) int { return cmp.Compare(a.Position, b.Position) })
	return out
}

// listLabel is a list's name; the archive and trash lists have none, so they
// go by their type.
func listLabel(l planka.List) string {
	if l.Name != nil {
		return *l.Name
	}
	return l.Type
}

// labelName is a label's display name: its name, or its colour for a label
// that has no name.
func labelName(l planka.Label) string {
	if l.Name != nil && strings.TrimSpace(*l.Name) != "" {
		return *l.Name
	}
	return l.Color
}

// userName is a user's display name: the full name, or the username.
func userName(u planka.User) string {
	if strings.TrimSpace(u.Name) != "" {
		return u.Name
	}
	return u.Username
}

// summary builds the short form of a card on this board. An unknown card
// yields a summary that holds only its ID.
func (s *boardSnapshot) summary(cardID string) CardSummary {
	c, ok := s.cards[cardID]
	if !ok {
		return CardSummary{ID: cardID}
	}
	out := CardSummary{
		ID:            c.ID,
		Title:         c.Name,
		Board:         s.resp.Item.Name,
		Due:           c.DueDate,
		DueCompleted:  c.IsDueCompleted != nil && *c.IsDueCompleted,
		CommentsTotal: c.CommentsTotal,
	}
	if l, ok := s.lists[c.ListID]; ok {
		out.List = listLabel(l)
	}
	labels := make([]planka.Label, 0, len(s.cardLabel[c.ID]))
	for _, id := range s.cardLabel[c.ID] {
		if l, ok := s.labels[id]; ok {
			labels = append(labels, l)
		}
	}
	slices.SortStableFunc(labels, func(a, b planka.Label) int { return cmp.Compare(a.Position, b.Position) })
	for _, l := range labels {
		out.Labels = append(out.Labels, labelName(l))
	}
	for _, id := range s.cardUser[c.ID] {
		if u, ok := s.users[id]; ok {
			out.Assignees = append(out.Assignees, userName(u))
		}
	}
	for _, t := range s.cardTasks[c.ID] {
		out.TasksTotal++
		if t.IsCompleted {
			out.TasksDone++
		}
	}
	return out
}

// cardsIn returns the summaries of the cards in a list, by position.
func (s *boardSnapshot) cardsIn(listID string) []CardSummary {
	var cards []planka.Card
	for _, c := range s.resp.Included.Cards {
		if c.ListID == listID {
			cards = append(cards, c)
		}
	}
	slices.SortStableFunc(cards, func(a, b planka.Card) int { return comparePosition(a.Position, b.Position) })
	out := make([]CardSummary, len(cards))
	for i, c := range cards {
		out[i] = s.summary(c.ID)
	}
	return out
}

// board returns the domain board with every kanban list.
func (s *boardSnapshot) board() *Board {
	return s.boardOf(s.kanbanLists())
}

// boardOf returns the domain board with only the given lists.
func (s *boardSnapshot) boardOf(lists []planka.List) *Board {
	b := &Board{
		ID:      s.resp.Item.ID,
		Name:    s.resp.Item.Name,
		Project: s.project,
		Lists:   make([]List, len(lists)),
	}
	for i, l := range lists {
		b.Lists[i] = List{ID: l.ID, Name: listLabel(l), Closed: l.Type == "closed", Cards: s.cardsIn(l.ID)}
	}
	for _, l := range s.sortedLabels() {
		b.Labels = append(b.Labels, labelName(l))
	}
	for _, m := range s.resp.Included.BoardMemberships {
		if u, ok := s.users[m.UserID]; ok {
			b.Members = append(b.Members, userName(u))
		}
	}
	return b
}
