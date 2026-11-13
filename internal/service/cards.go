package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	defaultSearchLimit = 20
	// maxCandidates is how many cards an AmbiguousError lists.
	maxCandidates = 10
)

// SearchQuery filters a card search. Text matches when every word of it is in
// the title or every word of it is in the description (Planka's own rule, so
// the archive agrees); words are split on spaces, commas and semicolons, and
// case is ignored. Title matches come first. Words may not be split between
// title and description. Every other non-empty field narrows the search.
// Board is an ID, a URL or a name; Project, List, Label and Assignee are
// names, and Assignee may be "me". Archived cards are searched only when
// IncludeArchived is set. Limit defaults to 20.
type SearchQuery struct {
	Text, Board, Project, List, Label, Assignee string
	IncludeArchived                             bool
	Limit                                       int
}

// CardRef points at a card: Ref is a card ID, a card URL or a title (or part
// of it). A title matches by the rule of SearchQuery.Text: all its words in a
// card's title, or all in its description. Board narrows a title search to one
// board. A title never resolves to an archived card unless IncludeArchived is
// set.
type CardRef struct {
	Ref, Board      string
	IncludeArchived bool
}

// Cards reads and writes cards: search, one card in full, its comments,
// create, move, update and delete.
type Cards struct {
	catalog  *Catalog
	boards   BoardsAPI
	cards    CardsReadAPI
	comments CommentsReadAPI
	users    UsersAPI
	write    CardsWriteAPI
	labels   LabelsAPI
	members  MembersAPI
	now      func() time.Time
	locks    boardLocks
}

// NewCards returns a Cards service that reads and writes through the given APIs.
func NewCards(
	catalog *Catalog, boards BoardsAPI, cards CardsReadAPI, comments CommentsReadAPI, users UsersAPI,
	write CardsWriteAPI, labels LabelsAPI, members MembersAPI,
) *Cards {
	return &Cards{
		catalog: catalog, boards: boards, cards: cards, comments: comments, users: users,
		write: write, labels: labels, members: members, now: time.Now,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// overlay returns a snapshot of the same board in which inc holds the cards
// and their labels, members, tasks and users. Users already on the board are
// not added twice, so a name still matches one user. Archived cards, and cards
// read one at a time, are not in the board's own snapshot.
func (s *boardSnapshot) overlay(inc planka.BoardIncluded) *boardSnapshot {
	r := *s.resp
	r.Included.Cards = inc.Cards
	r.Included.CardLabels = inc.CardLabels
	r.Included.CardMemberships = inc.CardMemberships
	r.Included.TaskLists = inc.TaskLists
	r.Included.Tasks = inc.Tasks
	r.Included.Users = slices.Clone(s.resp.Included.Users)
	for _, u := range inc.Users {
		if !slices.ContainsFunc(r.Included.Users, func(have planka.User) bool { return have.ID == u.ID }) {
			r.Included.Users = append(r.Included.Users, u)
		}
	}
	return newSnapshot(&r, s.project)
}

// archivedList is the list an archived card shows in. The parentheses keep it
// apart from a kanban list called "Archive".
const archivedList = "(archived)"

// cardSummary is snap.summary, except that an archived card shows in the list
// "(archived)" and a card in the trash in "Trash".
func cardSummary(snap *boardSnapshot, id string) CardSummary {
	out := snap.summary(id)
	if c, ok := snap.cards[id]; ok {
		switch snap.lists[c.ListID].Type {
		case archiveWord:
			out.List = archivedList
		case "trash":
			out.List = "Trash"
		}
	}
	return out
}

// resolve turns a write's card reference into the card's ID and the snapshot
// of its board. A write that plans from the board uses lockCard instead.
func (c *Cards) resolve(ctx context.Context, ref CardRef) (string, *boardSnapshot, error) {
	id, snap, err := c.resolveTarget(ctx, ref)
	if err != nil || snap != nil {
		return id, snap, err
	}
	boardID, err := c.cardBoardID(ctx, id)
	if err != nil {
		return "", nil, err
	}
	snap, err = c.loadSnapshot(ctx, boardID)
	return id, snap, err
}

// lockCard is resolve for a write that plans from what it reads: it finds the
// card's board, then locks and reads it with lockBoard. A title search reads
// the board before the lock is held, so the board is read again.
func (c *Cards) lockCard(ctx context.Context, ref CardRef) (id string, snap *boardSnapshot, unlock func(), err error) {
	id, boardID, err := c.cardBoard(ctx, ref)
	if err != nil {
		return "", nil, nil, err
	}
	snap, unlock, err = c.lockBoard(ctx, boardID)
	return id, snap, unlock, err
}

// cardBoard turns a write's card reference into the card's ID and its board's
// ID.
func (c *Cards) cardBoard(ctx context.Context, ref CardRef) (id, boardID string, err error) {
	id, snap, err := c.resolveTarget(ctx, ref)
	if err != nil {
		return "", "", err
	}
	if snap != nil {
		return id, snap.resp.Item.ID, nil
	}
	if boardID, err = c.cardBoardID(ctx, id); err != nil {
		return "", "", err
	}
	return id, boardID, nil
}

// lockBoard waits for the board's write lock (see boardLocks), then reads the
// board, so that a write plans from what the writes before it left. The
// caller calls unlock when its last write is done.
func (c *Cards) lockBoard(ctx context.Context, boardID string) (*boardSnapshot, func(), error) {
	unlock, err := c.locks.lock(ctx, boardID)
	if err != nil {
		return nil, nil, err
	}
	snap, err := c.loadSnapshot(ctx, boardID)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return snap, unlock, nil
}

// cardBoardID reads a card to learn its board's ID.
func (c *Cards) cardBoardID(ctx context.Context, id string) (string, error) {
	cr, err := c.cards.Card(ctx, id)
	if err != nil {
		return "", notFound("card", id, err)
	}
	return cr.Item.BoardID, nil
}

// resolveID turns a card reference into an ID, for a read. It reads nothing
// for an ID or URL. For a title it searches and returns the snapshot of the
// card's board.
func (c *Cards) resolveID(ctx context.Context, ref CardRef) (string, *boardSnapshot, error) {
	id, h, err := c.findCard(ctx, ref)
	if err != nil || h == nil {
		return id, nil, err
	}
	return h.ID, h.snap, nil
}

// resolveTarget is resolveID for a write. A search must find the card by its
// title: a card found only through its description is a DescriptionMatchError
// that names it.
func (c *Cards) resolveTarget(ctx context.Context, ref CardRef) (string, *boardSnapshot, error) {
	id, h, err := c.findCard(ctx, ref)
	if err != nil || h == nil {
		return id, nil, err
	}
	if h.tier != 0 {
		return "", nil, &DescriptionMatchError{Query: ref.Ref, Card: candidate(*h)}
	}
	return h.ID, h.snap, nil
}

// findCard returns the ID an ID or URL reference names, without reading
// anything, or else the one search hit a title reference matches. No hit is a
// NoMatchError and several are an AmbiguousError.
func (c *Cards) findCard(ctx context.Context, ref CardRef) (string, *hit, error) {
	if id, ok := parseRef(ref.Ref, "cards"); ok {
		return id, nil, nil
	}
	if strings.TrimSpace(ref.Ref) == "" {
		return "", nil, errors.New("card reference is empty")
	}
	hits, _, err := c.search(ctx, SearchQuery{
		Text: ref.Ref, Board: ref.Board, IncludeArchived: ref.IncludeArchived, Limit: maxCandidates + 1,
	})
	if err != nil {
		return "", nil, err
	}
	switch len(hits) {
	case 0:
		nm := &NoMatchError{Kind: "card", Query: ref.Ref, ArchiveHint: !ref.IncludeArchived}
		if ref.Board != "" {
			nm.Scope = fmt.Sprintf("board %q", ref.Board)
		}
		return "", nil, nm
	case 1:
		return "", &hits[0], nil
	}
	cands := make([]Candidate, min(len(hits), maxCandidates))
	for i := range cands {
		cands[i] = candidate(hits[i])
	}
	return "", nil, &AmbiguousError{Kind: "card", Query: ref.Ref, Candidates: cands}
}

// candidate names a search hit in an error: title, board and list, with its ID.
func candidate(h hit) Candidate {
	return Candidate{ID: h.ID, Label: fmt.Sprintf("%s — %s / %s", h.Title, h.Board, h.List)}
}

// loadSnapshot reads a board and indexes it. The project name comes with it.
func (c *Cards) loadSnapshot(ctx context.Context, boardID string) (*boardSnapshot, error) {
	resp, err := c.boards.Board(ctx, boardID)
	if err != nil {
		return nil, err
	}
	var project string
	for _, p := range resp.Included.Projects {
		if p.ID == resp.Item.ProjectID {
			project = p.Name
		}
	}
	return newSnapshot(resp, project), nil
}

// notFound turns a Planka 404 into ErrNotFound; any other error is unchanged.
func notFound(kind, id string, err error) error {
	var api *planka.APIError
	if errors.As(err, &api) && api.Status == http.StatusNotFound {
		return fmt.Errorf("%s %s: %w", kind, id, ErrNotFound)
	}
	return err
}
