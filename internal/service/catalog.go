package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// Catalog browses projects and boards.
type Catalog struct {
	projects ProjectsAPI
	boards   BoardsAPI
}

// NewCatalog returns a Catalog that reads through the given APIs.
func NewCatalog(projects ProjectsAPI, boards BoardsAPI) *Catalog {
	return &Catalog{projects: projects, boards: boards}
}

// ListBoards lists the projects and boards the user can see. A non-empty query
// keeps the boards whose project or board name contains it, ignoring case: a
// matching project keeps all its boards, and a project left without boards is
// dropped.
func (c *Catalog) ListBoards(ctx context.Context, query string) ([]ProjectBoards, error) {
	resp, err := c.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	q := normalize(query)
	groups := groupBoards(resp)
	out := make([]ProjectBoards, 0, len(groups))
	for _, g := range groups {
		if q == "" || strings.Contains(normalize(g.Project), q) {
			out = append(out, g)
			continue
		}
		var kept []BoardRef
		for _, b := range g.Boards {
			if strings.Contains(normalize(b.Name), q) {
				kept = append(kept, b)
			}
		}
		if len(kept) > 0 {
			out = append(out, ProjectBoards{Project: g.Project, Boards: kept})
		}
	}
	return out, nil
}

// GetBoard returns a board with its kanban lists and their cards. ref is a
// board ID, a board URL or a board name; a non-empty list keeps only that list.
func (c *Catalog) GetBoard(ctx context.Context, ref, list string) (*Board, error) {
	br, err := c.resolveBoard(ctx, ref)
	if err != nil {
		return nil, err
	}
	resp, err := c.boards.Board(ctx, br.ID)
	if err != nil {
		return nil, err
	}
	snap := newSnapshot(resp, br.Project)
	if list == "" {
		return snap.board(), nil
	}
	l, err := snap.kanbanListByName(list)
	if err != nil {
		return nil, err
	}
	return snap.boardOf([]planka.List{l}), nil
}

// resolveBoard turns an ID, a URL or a name into a board. A name is matched
// across all projects; one that fits boards in several projects is an
// AmbiguousError that labels each candidate "Project / Board".
func (c *Catalog) resolveBoard(ctx context.Context, ref string) (BoardRef, error) {
	resp, err := c.projects.Projects(ctx)
	if err != nil {
		return BoardRef{}, err
	}
	var refs []BoardRef
	for _, g := range groupBoards(resp) {
		refs = append(refs, g.Boards...)
	}
	return pickBoard(refs, ref)
}

// pickBoard finds the board ref (an ID, a URL or a name) among refs.
func pickBoard(refs []BoardRef, ref string) (BoardRef, error) {
	id, isRef := parseRef(ref, "boards")
	if isRef {
		if i := slices.IndexFunc(refs, func(b BoardRef) bool { return b.ID == id }); i >= 0 {
			return refs[i], nil
		}
		// A purely numeric reference may also be a board called "2026".
		if normalize(ref) != id {
			return BoardRef{}, fmt.Errorf("board %s: %w", id, ErrNotFound)
		}
	}
	b, err := boardByName(refs, ref)
	var unk *UnknownNameError
	if isRef && errors.As(err, &unk) {
		return BoardRef{}, fmt.Errorf("board %s: %w", id, ErrNotFound)
	}
	return b, err
}

// boardLabel is how a board is shown when its name alone is not unique.
func boardLabel(b BoardRef) string { return b.Project + " / " + b.Name }

// boardByName matches ref against "Project / Board" labels exactly, then
// against board names. Several equal matches are an AmbiguousError.
func boardByName(refs []BoardRef, ref string) (BoardRef, error) {
	labels := make([]string, len(refs))
	names := make([]string, len(refs))
	for i, b := range refs {
		labels[i], names[i] = boardLabel(b), b.Name
	}
	var found []int
	for i, l := range labels {
		if normalize(l) == normalize(ref) {
			found = append(found, i)
		}
	}
	if len(found) == 0 {
		found = findMatches(ref, names)
	}
	switch len(found) {
	case 0:
		return BoardRef{}, &UnknownNameError{Kind: "board", Name: ref, Valid: labels}
	case 1:
		return refs[found[0]], nil
	default:
		cands := make([]Candidate, len(found))
		for i, idx := range found {
			cands[i] = Candidate{ID: refs[idx].ID, Label: labels[idx]}
		}
		return BoardRef{}, &AmbiguousError{Kind: "board", Query: ref, Candidates: cands}
	}
}

// groupBoards groups the boards by project, in project order, each project's
// boards by position.
func groupBoards(resp *planka.ProjectsResponse) []ProjectBoards {
	byProject := map[string][]planka.Board{}
	for _, b := range resp.Included.Boards {
		byProject[b.ProjectID] = append(byProject[b.ProjectID], b)
	}
	out := make([]ProjectBoards, 0, len(resp.Items))
	for _, p := range resp.Items {
		boards := byProject[p.ID]
		slices.SortStableFunc(boards, func(a, b planka.Board) int { return cmp.Compare(a.Position, b.Position) })
		refs := make([]BoardRef, len(boards))
		for i, b := range boards {
			refs[i] = BoardRef{ID: b.ID, Name: b.Name, Project: p.Name}
		}
		out = append(out, ProjectBoards{Project: p.Name, Boards: refs})
	}
	return out
}
