package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	// searchConcurrency is how many boards a search fetches at once.
	searchConcurrency = 4
	// maxArchiveSearch is the longest text Planka's archive search accepts.
	maxArchiveSearch = 128
	// archiveRank sorts archived hits after every kanban list of their board.
	archiveRank = 1 << 20
)

// hit is a card a search found, with the snapshot of the board it is on.
type hit struct {
	CardSummary
	snap              *boardSnapshot
	tier, board, list int
	listChangedAt     time.Time
	pos               *float64
}

// searchFilter is a SearchQuery prepared for matching.
type searchFilter struct {
	q     SearchQuery
	words []string
	meID  string
	named bool // a board was named, so an unknown list or label is an error
}

// boardMatcher is a searchFilter resolved against one board.
type boardMatcher struct {
	listID, labelID, userID string
}

// boardResult is what searching one board gave: hits, or the reason the board
// was left out because it lacks the list, label or user asked for.
type boardResult struct {
	hits    []hit
	skipped error
}

// Search finds cards across the boards the user can see. Cards that match all
// the words of the text in their title come before those that match in the
// description; within a tier they follow board, list and card order. more
// counts the matches the limit left out.
func (c *Cards) Search(ctx context.Context, q SearchQuery) (cards []CardSummary, more int, err error) {
	hits, more, err := c.search(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	cards = make([]CardSummary, len(hits))
	for i, h := range hits {
		cards[i] = h.CardSummary
	}
	return cards, more, nil
}

func (c *Cards) search(ctx context.Context, q SearchQuery) ([]hit, int, error) {
	if q.Limit <= 0 {
		q.Limit = defaultSearchLimit
	}
	if q.IncludeArchived {
		if n := utf8.RuneCountInString(archiveText(q.Text)); n > maxArchiveSearch {
			return nil, 0, fmt.Errorf("search text is %d characters, but searching the archive allows at most %d", n, maxArchiveSearch)
		}
	}
	targets, err := c.candidateBoards(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	f := searchFilter{
		q:     q,
		words: searchWords(q.Text),
		named: q.Board != "",
	}
	if normalize(q.Assignee) == "me" {
		me, err := c.users.Me(ctx)
		if err != nil {
			return nil, 0, err
		}
		f.meID = me.ID
	}

	results := make([]boardResult, len(targets))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(searchConcurrency)
	for i, br := range targets {
		g.Go(func() error {
			r, err := c.searchBoard(gctx, br, i, f)
			results[i] = r
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, err
	}
	return rankHits(results, q.Limit)
}

// rankHits merges the per-board results, sorted and cut to limit, and counts
// the hits the cut left out. When every board was left out, it returns the
// reason the first one was.
func rankHits(results []boardResult, limit int) ([]hit, int, error) {
	var hits []hit
	var skipped []error
	for _, r := range results {
		hits = append(hits, r.hits...)
		if r.skipped != nil {
			skipped = append(skipped, r.skipped)
		}
	}
	if len(skipped) > 0 && len(skipped) == len(results) {
		return nil, 0, skipped[0]
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(a.tier, b.tier), cmp.Compare(a.board, b.board),
			cmp.Compare(a.list, b.list), comparePosition(a.pos, b.pos),
			b.listChangedAt.Compare(a.listChangedAt), cmp.Compare(a.ID, b.ID))
	})
	more := max(len(hits)-limit, 0)
	return hits[:len(hits)-more], more, nil
}

// candidateBoards lists the boards a search covers, in project and board order.
func (c *Cards) candidateBoards(ctx context.Context, q SearchQuery) ([]BoardRef, error) {
	resp, err := c.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	groups := groupBoards(resp)
	if q.Project != "" {
		g, err := matchName("project", q.Project, "", groups, func(g ProjectBoards) string { return g.Project }, nil)
		if err != nil {
			return nil, err
		}
		groups = []ProjectBoards{g}
	}
	var refs []BoardRef
	for _, g := range groups {
		refs = append(refs, g.Boards...)
	}
	if q.Board == "" {
		return refs, nil
	}
	b, err := pickBoard(refs, q.Board)
	if err != nil {
		return nil, err
	}
	return []BoardRef{b}, nil
}

// searchBoard fetches a board and finds the matching cards on it, archived
// ones too when asked. idx is the board's place in the search order.
func (c *Cards) searchBoard(ctx context.Context, br BoardRef, idx int, f searchFilter) (boardResult, error) {
	resp, err := c.boards.Board(ctx, br.ID)
	if err != nil {
		return boardResult{}, err
	}
	snap := newSnapshot(resp, br.Project)
	m, err := f.matcherFor(snap)
	if err != nil {
		var unknown *UnknownNameError
		if !f.named && errors.As(err, &unknown) {
			return boardResult{skipped: err}, nil
		}
		return boardResult{}, err
	}
	hits := f.scan(snap, snap, idx, m)
	// No archived card is on a kanban list, so a list filter rules them out.
	if f.q.IncludeArchived && m.listID == "" {
		view, err := c.archiveView(ctx, snap, f.q.Text)
		if err != nil {
			return boardResult{}, err
		}
		if view != nil {
			hits = append(hits, f.scan(view, snap, idx, m)...)
		}
	}
	return boardResult{hits: hits}, nil
}

// archiveView reads the board's archived cards that match text, as a snapshot
// that holds only them. It is nil when the board has no archive list.
func (c *Cards) archiveView(ctx context.Context, snap *boardSnapshot, text string) (*boardSnapshot, error) {
	arch := snap.archiveList()
	if arch == nil {
		return nil, nil
	}
	resp, err := c.cards.ListCards(ctx, arch.ID, planka.CardQuery{Search: archiveText(text)})
	if err != nil {
		return nil, err
	}
	return snap.overlay(planka.BoardIncluded{
		Cards:           resp.Items,
		CardLabels:      resp.Included.CardLabels,
		CardMemberships: resp.Included.CardMemberships,
		Users:           resp.Included.Users,
	}), nil
}

// matcherFor resolves the list, label and assignee names against one board.
func (f searchFilter) matcherFor(snap *boardSnapshot) (boardMatcher, error) {
	var m boardMatcher
	if f.q.List != "" {
		l, err := snap.kanbanListByName(f.q.List)
		if err != nil {
			return m, err
		}
		m.listID = l.ID
	}
	if f.q.Label != "" {
		l, err := snap.labelByName(f.q.Label)
		if err != nil {
			return m, err
		}
		m.labelID = l.ID
	}
	if f.q.Assignee != "" {
		u, err := snap.userByName(f.q.Assignee, f.meID)
		if err != nil {
			return m, err
		}
		m.userID = u.ID
	}
	return m, nil
}

// scan returns the cards of view that pass the filter. owner is the board's
// own snapshot: it fixes the list order and is what a hit carries.
func (f searchFilter) scan(view, owner *boardSnapshot, boardIdx int, m boardMatcher) []hit {
	listIdx := map[string]int{}
	for i, l := range owner.kanbanLists() {
		listIdx[l.ID] = i
	}
	var hits []hit
	for _, card := range view.resp.Included.Cards {
		tier := f.tier(card)
		if tier < 0 ||
			(m.listID != "" && card.ListID != m.listID) ||
			(m.labelID != "" && !slices.Contains(view.cardLabel[card.ID], m.labelID)) ||
			(m.userID != "" && !slices.Contains(view.cardUser[card.ID], m.userID)) {
			continue
		}
		rank, ok := listIdx[card.ListID]
		if !ok {
			rank = archiveRank
		}
		hits = append(hits, hit{
			CardSummary: cardSummary(view, card.ID), snap: owner,
			tier: tier, board: boardIdx, list: rank, pos: card.Position,
			listChangedAt: card.ListChangedAt,
		})
	}
	return hits
}

// tier is 0 when the title holds every word of the text, 1 when the
// description does, and -1 when the card does not match. This is Planka's own
// rule for the archive search, so local and server results agree.
func (f searchFilter) tier(c planka.Card) int {
	switch {
	case containsAll(strings.ToLower(c.Name), f.words):
		return 0
	case containsAll(strings.ToLower(deref(c.Description)), f.words):
		return 1
	default:
		return -1
	}
}

// searchWords splits text into lower-case words the way Planka does: on
// spaces, commas and semicolons.
func searchWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == ',' || r == ';'
	})
}

// archiveText is the text sent to Planka's archive search. Planka reads text
// that starts with "/" as a regular expression, so a space goes in front of it;
// Planka drops empty words.
func archiveText(text string) string {
	if strings.HasPrefix(text, "/") {
		return " " + text
	}
	return text
}

func containsAll(s string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
