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

// ListColors are the colours Planka accepts for a list.
var ListColors = []string{
	"berry-red", "pumpkin-orange", "lagoon-blue", "pink-tulip", "light-mud",
	"orange-peel", "bright-moss", "antique-blue", "dark-granite", "turquoise-sea",
}

// NoColor clears a list's colour in UpdateList.Color.
const NoColor = "none"

// ListSorts are the values UpdateList.SortBy takes, and SortOrders those of
// UpdateList.SortOrder.
var (
	ListSorts  = []string{"name", "due_date", "created"}
	SortOrders = []string{"asc", "desc"}
)

// sortFields maps each of ListSorts to Planka's field name.
var sortFields = map[string]string{"name": "name", "due_date": "dueDate", "created": "createdAt"}

// maxListName is the longest list name Planka stores, in UTF-16 units.
const maxListName = 128

// Lists adds, changes and deletes the lists (columns) of a board. Reading them
// is (*Catalog).GetBoard.
type Lists struct {
	cards *Cards
	api   ListsAPI
}

// NewLists returns a Lists service. It finds boards and holds their write
// locks through cards, and writes through api.
func NewLists(cards *Cards, api ListsAPI) *Lists {
	return &Lists{cards: cards, api: api}
}

// ListPlace puts a list (or a checklist) right after or right before another
// one, given by ID or exact name. With both empty a new one goes last and a
// moved one stays.
type ListPlace struct {
	After, Before string
}

// CreateList describes a new list. Board is an ID, a URL or a name. A Closed
// list marks the cards in it as done. Color is one of ListColors, or empty.
type CreateList struct {
	Board, Name string
	Place       ListPlace
	Closed      bool
	Color       string
}

// UpdateList changes a list, found on Board by ID or exact name. A nil field
// and an empty string are left alone. Color NoColor clears the colour. SortBy
// is one of ListSorts, SortOrder one of SortOrders ("asc" when empty).
// ArchiveCards moves every card of a closed list to the board's archive.
type UpdateList struct {
	Board, List       string
	Name              *string
	Color             string
	Closed            *bool
	Place             ListPlace
	SortBy, SortOrder string
	ArchiveCards      bool
}

// Column is a list as a write leaves it. Cards counts its cards.
type Column struct {
	ID, Name, Color string
	Closed          bool
	Cards           int
}

// ListResult is a list after a write. Order names the board's lists in board
// order. Archived counts the cards ArchiveCards moved.
type ListResult struct {
	List     Column
	Order    []string
	Archived int
}

// DeletedList is a deleted list and how many of its cards Planka moved to the
// board's trash.
type DeletedList struct {
	ID, Name     string
	CardsToTrash int
}

// Create adds a list to a board, after or before another list, or last.
func (s *Lists) Create(ctx context.Context, in CreateList) (*ListResult, error) {
	name, err := listName(in.Name)
	if err != nil {
		return nil, err
	}
	if err := checkListColor(in.Color, false); err != nil {
		return nil, err
	}
	snap, unlock, err := s.cards.lockBoardRef(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := uniqueListName(snap, name, ""); err != nil {
		return nil, err
	}
	pos, err := listPosition(snap, in.Place, "")
	if err != nil {
		return nil, err
	}

	req := planka.CreateListRequest{Type: listType(in.Closed), Position: pos, Name: name}
	created, err := s.api.CreateList(ctx, snap.resp.Item.ID, req)
	if err != nil {
		return nil, err
	}
	if in.Color != "" {
		if _, err := s.api.UpdateList(ctx, created.ID, planka.UpdateListRequest{Color: planka.Some(in.Color)}); err != nil {
			return nil, fmt.Errorf("list %q (id %s) was created, but setting its color failed: %w", name, created.ID, err)
		}
	}
	return s.written(ctx, snap, created.ID, name, 0, "created")
}

// Update changes a list's fields, then sorts its cards, then archives them,
// as in asks. Every check runs before the first write.
func (s *Lists) Update(ctx context.Context, in UpdateList) (*ListResult, error) {
	if err := checkListUpdate(in); err != nil {
		return nil, err
	}
	snap, unlock, err := s.cards.lockBoardRef(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	list, err := exactList(snap, in.List)
	if err != nil {
		return nil, err
	}
	patch, err := listPatch(snap, list, in)
	if err != nil {
		return nil, err
	}
	archive, err := archiveTarget(snap, list, in)
	if err != nil {
		return nil, err
	}
	archived, err := s.apply(ctx, list, patch, in, archive)
	if err != nil {
		return nil, err
	}
	return s.written(ctx, snap, list.ID, listLabel(list), archived, "updated")
}

// archiveTarget is the board's archive list when in archives the list's
// cards, else nil. Planka moves all of a list's cards only from a closed
// list, so the list must be closed already or closed by in.
func archiveTarget(snap *boardSnapshot, list planka.List, in UpdateList) (*planka.List, error) {
	if !in.ArchiveCards {
		return nil, nil
	}
	closed := list.Type == "closed"
	if in.Closed != nil {
		closed = *in.Closed
	}
	if !closed {
		return nil, fmt.Errorf("list %q is not closed: Planka archives all of a list's cards only from a closed list; "+
			"set closed=true too, or move the cards one by one", listLabel(list))
	}
	archive := snap.archiveList()
	if archive == nil {
		return nil, fmt.Errorf("%s has no archive list", snap.scope())
	}
	return archive, nil
}

// apply makes the writes of Update, the archive only when archive is not nil.
// It returns how many cards Planka archived. A failure after a write says what
// was already applied.
func (s *Lists) apply(
	ctx context.Context, list planka.List, patch planka.UpdateListRequest, in UpdateList, archive *planka.List,
) (int, error) {
	var done []string
	failed := func(step string, err error) error {
		if len(done) == 0 {
			return err
		}
		return fmt.Errorf("list %q was partly updated (%s), but %s failed: %w", listLabel(list), strings.Join(done, ", "), step, err)
	}
	if patch != (planka.UpdateListRequest{}) {
		if _, err := s.api.UpdateList(ctx, list.ID, patch); err != nil {
			return 0, failed("the update", err)
		}
		done = append(done, "fields updated")
	}
	if in.SortBy != "" {
		if err := s.api.SortList(ctx, list.ID, sortFields[in.SortBy], cmp.Or(in.SortOrder, "asc")); err != nil {
			return 0, failed("sorting its cards", err)
		}
		done = append(done, "cards sorted")
	}
	if archive == nil {
		return 0, nil
	}
	moved, err := s.api.MoveListCards(ctx, list.ID, archive.ID)
	if err != nil {
		return 0, failed("archiving its cards", err)
	}
	return len(moved), nil
}

// Delete deletes a list, found on board by ID or exact name. Planka moves its
// cards to the board's trash.
func (s *Lists) Delete(ctx context.Context, board, ref string) (*DeletedList, error) {
	snap, unlock, err := s.cards.lockBoardRef(ctx, board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	list, err := exactList(snap, ref)
	if err != nil {
		return nil, err
	}
	moved, err := s.api.DeleteList(ctx, list.ID)
	if err != nil {
		return nil, notFound("list", list.ID, err)
	}
	return &DeletedList{ID: list.ID, Name: listLabel(list), CardsToTrash: len(moved)}, nil
}

// written reads the board again and returns the list as it is now, with the
// board's lists in order. The write already happened (verb says which), so a
// failed read says so; otherwise a retry would repeat the write.
func (s *Lists) written(ctx context.Context, snap *boardSnapshot, id, name string, archived int, verb string) (*ListResult, error) {
	fresh, err := s.cards.loadSnapshot(ctx, snap.resp.Item.ID)
	if err != nil {
		return nil, fmt.Errorf("list %q (id %s) was %s, but reading the board back failed: %w", name, id, verb, err)
	}
	out := &ListResult{Archived: archived}
	for _, l := range fresh.kanbanLists() {
		out.Order = append(out.Order, listLabel(l))
		if l.ID == id {
			out.List = Column{ID: l.ID, Name: listLabel(l), Color: deref(l.Color), Closed: l.Type == "closed", Cards: len(fresh.cardsIn(l.ID))}
		}
	}
	return out, nil
}

// checkListUpdate checks the fields of in that need no board: something to
// change, a valid name, colour and sort.
func checkListUpdate(in UpdateList) error {
	if in.Name == nil && in.Color == "" && in.Closed == nil && in.Place == (ListPlace{}) &&
		in.SortBy == "" && in.SortOrder == "" && !in.ArchiveCards {
		return errors.New("nothing to change: give name, color, closed, after, before, sort_cards or archive_cards")
	}
	if in.Name != nil {
		if _, err := listName(*in.Name); err != nil {
			return err
		}
	}
	if err := checkListColor(in.Color, true); err != nil {
		return err
	}
	return checkListSort(in.SortBy, in.SortOrder)
}

// checkListSort accepts no sort, or one of ListSorts with an optional order.
func checkListSort(by, order string) error {
	switch {
	case by == "" && order != "":
		return errors.New("sort_order needs sort_cards")
	case by == "":
		return nil
	}
	if !slices.Contains(ListSorts, by) {
		return fmt.Errorf("unknown sort %q: use %s", by, orList(ListSorts))
	}
	if order != "" && !slices.Contains(SortOrders, order) {
		return fmt.Errorf("unknown sort_order %q: use %s", order, orList(SortOrders))
	}
	return nil
}

// listPatch is the PATCH a list update sends: the fields that change. It
// checks a new name against the board's other lists and places a move. The
// name was checked by checkListUpdate.
func listPatch(snap *boardSnapshot, list planka.List, in UpdateList) (planka.UpdateListRequest, error) {
	var patch planka.UpdateListRequest
	if in.Name != nil {
		if name := strings.TrimSpace(*in.Name); name != listLabel(list) {
			if err := uniqueListName(snap, name, list.ID); err != nil {
				return patch, err
			}
			patch.Name = planka.Some(name)
		}
	}
	if in.Closed != nil {
		if typ := listType(*in.Closed); typ != list.Type {
			patch.Type = planka.Some(typ)
		}
	}
	switch in.Color {
	case "":
	case NoColor:
		if list.Color != nil {
			patch.Color = planka.Null[string]()
		}
	default:
		if deref(list.Color) != in.Color {
			patch.Color = planka.Some(in.Color)
		}
	}
	if in.Place != (ListPlace{}) {
		pos, err := listPosition(snap, in.Place, list.ID)
		if err != nil {
			return patch, err
		}
		patch.Position = planka.Some(pos)
	}
	return patch, nil
}

// listType is Planka's type of a closed or an active list.
func listType(closed bool) string {
	if closed {
		return "closed"
	}
	return "active"
}

// listName trims a list name and checks it is not empty or too long.
func listName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("list name is empty")
	case jsLen(name) > maxListName:
		return "", fmt.Errorf("list name is longer than %d characters", maxListName)
	}
	return name, nil
}

// checkListColor accepts "", one of ListColors, and with allowNone also NoColor.
func checkListColor(color string, allowNone bool) error {
	if color == "" || slices.Contains(ListColors, color) || (allowNone && color == NoColor) {
		return nil
	}
	valid := strings.Join(ListColors, ", ")
	if allowNone {
		valid += ", or " + NoColor + " to clear it"
	}
	return fmt.Errorf("unknown list color %q; colors: %s", color, valid)
}

// uniqueListName refuses a name another list on the board already has, case
// ignored; skip is the list being renamed. Two lists of one name would make
// every later reference to it ambiguous.
func uniqueListName(snap *boardSnapshot, name, skip string) error {
	for _, l := range snap.kanbanLists() {
		if l.ID != skip && normalize(listLabel(l)) == normalize(name) {
			return fmt.Errorf("%s already has a list %q (id %s): pick another name", snap.scope(), listLabel(l), l.ID)
		}
	}
	return nil
}

// exactList finds the list a write targets: by ID, or by its exact name, case
// and surrounding space ignored. A prefix, which reads accept, does not count:
// a write must not hit the wrong list.
func exactList(snap *boardSnapshot, ref string) (planka.List, error) {
	lists := snap.kanbanLists()
	if l, ok := byID(ref, lists, listID); ok {
		return l, nil
	}
	var found []planka.List
	for _, l := range lists {
		if normalize(listLabel(l)) == normalize(ref) {
			found = append(found, l)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		names := make([]string, len(lists))
		for i, l := range lists {
			names[i] = listLabel(l)
		}
		return planka.List{}, &UnknownNameError{Kind: "list", Name: strings.TrimSpace(ref), Scope: snap.scope(), Valid: names}
	}
	cands := make([]Candidate, len(found))
	for i, l := range found {
		cands[i] = Candidate{ID: l.ID, Label: listLabel(l)}
	}
	return planka.List{}, &AmbiguousError{Kind: "list", Query: ref, Candidates: cands}
}

// listPosition is the position that puts a list where place says among the
// board's lists (see between), leaving out skip, the list being moved. With
// place empty the list goes last. A list without a position sorts last, so it
// has no neighbours to go between: a place next to one is the end.
func listPosition(snap *boardSnapshot, place ListPlace, skip string) (float64, error) {
	if place.After != "" && place.Before != "" {
		return 0, errors.New("give after or before, not both")
	}
	var ids []string
	var pos []float64
	for _, l := range snap.kanbanLists() {
		if l.ID != skip && l.Position != nil {
			ids = append(ids, l.ID)
			pos = append(pos, *l.Position)
		}
	}
	ref := cmp.Or(place.After, place.Before)
	if ref == "" {
		return bottomPosition(pos), nil
	}
	anchor, err := exactList(snap, ref)
	if err != nil {
		return 0, err
	}
	if anchor.ID == skip {
		return 0, errors.New("a list cannot go after or before itself")
	}
	i := slices.Index(ids, anchor.ID)
	if i < 0 {
		return bottomPosition(pos), nil
	}
	return between(pos, i, place.After != ""), nil
}

// between is the position right after (or right before) pos[i] among the
// sorted positions pos: halfway to its neighbour, or past the end as
// bottomPosition and topPosition put it.
func between(pos []float64, i int, after bool) float64 {
	switch {
	case after && i == len(pos)-1:
		return bottomPosition(pos)
	case after:
		return (pos[i] + pos[i+1]) / 2
	case i == 0:
		return topPosition(pos)
	default:
		return (pos[i-1] + pos[i]) / 2
	}
}
