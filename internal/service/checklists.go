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

// maxChecklistName is the longest checklist name Planka stores, in UTF-16 units.
const maxChecklistName = 128

// CreateChecklist describes a new checklist on Card. Place puts it after or
// before another checklist, by ID or exact name; with both empty it goes last.
// A nil display option takes Planka's default: shown on the front of the card,
// completed tasks shown.
type CreateChecklist struct {
	Card                                  CardRef
	Name                                  string
	Place                                 ListPlace
	ShowOnFrontOfCard, HideCompletedTasks *bool
}

// UpdateChecklist changes the checklist Checklist (an ID or exact name) on
// Card. A nil field and an empty Place are left alone.
type UpdateChecklist struct {
	Card                                  CardRef
	Checklist                             string
	Name                                  *string
	Place                                 ListPlace
	ShowOnFrontOfCard, HideCompletedTasks *bool
}

// ChecklistResult is a checklist after a write, with its tasks, and the names
// of the card's checklists in order.
type ChecklistResult struct {
	Checklist TaskList
	Order     []string
}

// DeletedChecklist is a deleted checklist with the tasks Planka deleted with
// it, as they were.
type DeletedChecklist struct {
	ID, Name string
	Tasks    []Task
}

// CreateChecklist adds a checklist to a card. Its name must differ from the
// card's other checklists, case ignored.
func (t *Tasks) CreateChecklist(ctx context.Context, in CreateChecklist) (*ChecklistResult, error) {
	name, err := checklistName(in.Name)
	if err != nil {
		return nil, err
	}
	id, snap, cr, unlock, err := t.lockChecklists(ctx, in.Card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	lists := sortedChecklists(cr)
	if err := uniqueChecklistName(cr, lists, name, ""); err != nil {
		return nil, err
	}
	pos, err := checklistPosition(cr, lists, in.Place, "")
	if err != nil {
		return nil, err
	}
	created, err := t.api.CreateTaskList(ctx, id, planka.CreateTaskListRequest{
		Name: name, Position: pos, ShowOnFrontOfCard: in.ShowOnFrontOfCard, HideCompletedTasks: in.HideCompletedTasks,
	})
	if err != nil {
		return nil, err
	}
	return t.checklistWritten(ctx, id, snap, created.ID, name, "created")
}

// UpdateChecklist renames or moves a checklist, or changes how it shows.
func (t *Tasks) UpdateChecklist(ctx context.Context, in UpdateChecklist) (*ChecklistResult, error) {
	if in.Name == nil && in.Place == (ListPlace{}) && in.ShowOnFrontOfCard == nil && in.HideCompletedTasks == nil {
		return nil, errors.New("nothing to change: give name, after, before, show_on_front_of_card or hide_completed_tasks")
	}
	if in.Name != nil {
		if _, err := checklistName(*in.Name); err != nil {
			return nil, err
		}
	}
	id, snap, cr, unlock, err := t.lockChecklists(ctx, in.Card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	lists := sortedChecklists(cr)
	target, err := exactChecklist(cr, lists, in.Checklist)
	if err != nil {
		return nil, err
	}
	patch, err := checklistPatch(cr, lists, target, in)
	if err != nil {
		return nil, err
	}
	if patch != (planka.UpdateTaskListRequest{}) {
		if _, err := t.api.UpdateTaskList(ctx, target.ID, patch); err != nil {
			return nil, notFound("checklist", target.ID, err)
		}
	}
	return t.checklistWritten(ctx, id, snap, target.ID, target.Name, "updated")
}

// DeleteChecklist deletes a checklist. Planka deletes its tasks with it and
// keeps no copy, so a checklist that still has tasks is refused unless
// withTasks is set; the result lists the tasks it had.
func (t *Tasks) DeleteChecklist(ctx context.Context, card CardRef, checklist string, withTasks bool) (*DeletedChecklist, error) {
	_, snap, cr, unlock, err := t.lockChecklists(ctx, card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	target, err := exactChecklist(cr, sortedChecklists(cr), checklist)
	if err != nil {
		return nil, err
	}
	gone := toTaskList(target, cr.Included.Tasks, boardView(cr, snap))
	if n := len(gone.Tasks); n > 0 && !withTasks {
		return nil, fmt.Errorf("checklist %q has %d tasks, and deleting it deletes them too, for good: "+
			"move them first with update_tasks, or set with_tasks=true", target.Name, n)
	}
	if err := t.api.DeleteTaskList(ctx, target.ID); err != nil {
		return nil, notFound("checklist", target.ID, err)
	}
	return &DeletedChecklist{ID: target.ID, Name: target.Name, Tasks: gone.Tasks}, nil
}

// lockChecklists finds a card, locks its board and reads the card under the
// lock, so a write plans from the checklists the writes before it left.
func (t *Tasks) lockChecklists(ctx context.Context, ref CardRef) (id string, snap *boardSnapshot, cr *planka.CardResponse, unlock func(), err error) {
	id, snap, unlock, err = t.cards.lockCard(ctx, ref)
	if err != nil {
		return "", nil, nil, nil, err
	}
	if cr, err = t.cards.cards.Card(ctx, id); err != nil {
		unlock()
		return "", nil, nil, nil, notFound("card", id, err)
	}
	return id, snap, cr, unlock, nil
}

// checklistWritten reads the card again and returns the checklist listID as it
// is now. The write already happened (verb says which), so a failed read says
// so; otherwise a retry would repeat the write.
func (t *Tasks) checklistWritten(ctx context.Context, cardID string, snap *boardSnapshot, listID, name, verb string) (*ChecklistResult, error) {
	fresh, err := t.cards.cards.Card(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("checklist %q (id %s) was %s, but reading the card back failed: %w", name, listID, verb, err)
	}
	out := &ChecklistResult{}
	for _, tl := range sortedChecklists(fresh) {
		out.Order = append(out.Order, tl.Name)
		if tl.ID == listID {
			out.Checklist = toTaskList(tl, fresh.Included.Tasks, boardView(fresh, snap))
		}
	}
	return out, nil
}

// checklistPatch is the PATCH a checklist update sends: the fields that
// change. A new name is checked against the card's other checklists.
func checklistPatch(cr *planka.CardResponse, lists []planka.TaskList, target planka.TaskList, in UpdateChecklist) (planka.UpdateTaskListRequest, error) {
	var patch planka.UpdateTaskListRequest
	if in.Name != nil {
		if name := strings.TrimSpace(*in.Name); name != target.Name {
			if err := uniqueChecklistName(cr, lists, name, target.ID); err != nil {
				return patch, err
			}
			patch.Name = planka.Some(name)
		}
	}
	if in.Place != (ListPlace{}) {
		pos, err := checklistPosition(cr, lists, in.Place, target.ID)
		if err != nil {
			return patch, err
		}
		patch.Position = planka.Some(pos)
	}
	if in.ShowOnFrontOfCard != nil && *in.ShowOnFrontOfCard != target.ShowOnFrontOfCard {
		patch.ShowOnFrontOfCard = planka.Some(*in.ShowOnFrontOfCard)
	}
	if in.HideCompletedTasks != nil && *in.HideCompletedTasks != target.HideCompletedTasks {
		patch.HideCompletedTasks = planka.Some(*in.HideCompletedTasks)
	}
	return patch, nil
}

// checklistName trims a checklist name and checks it is not empty or too long.
func checklistName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("checklist name is empty")
	case jsLen(name) > maxChecklistName:
		return "", fmt.Errorf("checklist name is longer than %d characters", maxChecklistName)
	}
	return name, nil
}

// sortedChecklists is the card's checklists in Planka's order: by position,
// ties by ID.
func sortedChecklists(cr *planka.CardResponse) []planka.TaskList {
	lists := slices.Clone(cr.Included.TaskLists)
	slices.SortStableFunc(lists, func(a, b planka.TaskList) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(len(a.ID), len(b.ID)), cmp.Compare(a.ID, b.ID))
	})
	return lists
}

// cardScope names the card of cr in an error.
func cardScope(cr *planka.CardResponse) string {
	return fmt.Sprintf("card %q", cr.Item.Name)
}

// exactChecklist finds the checklist a write targets: by ID, or by its exact
// name, case and surrounding space ignored. A prefix does not count.
func exactChecklist(cr *planka.CardResponse, lists []planka.TaskList, ref string) (planka.TaskList, error) {
	if strings.TrimSpace(ref) == "" {
		return planka.TaskList{}, errors.New("checklist is empty: give its name or ID")
	}
	if l, ok := byID(ref, lists, taskListID); ok {
		return l, nil
	}
	found, err := findTaskList(lists, ref)
	switch {
	case err != nil:
		var amb *AmbiguousError
		if errors.As(err, &amb) {
			amb.Kind = "checklist"
		}
		return planka.TaskList{}, err
	case found != nil:
		return *found, nil
	}
	names := make([]string, len(lists))
	for i, l := range lists {
		names[i] = l.Name
	}
	return planka.TaskList{}, &UnknownNameError{Kind: "checklist", Name: strings.TrimSpace(ref), Scope: cardScope(cr), Valid: names}
}

func taskListID(l planka.TaskList) string { return l.ID }

// uniqueChecklistName refuses a name another checklist on the card already
// has, case ignored; skip is the checklist being renamed.
func uniqueChecklistName(cr *planka.CardResponse, lists []planka.TaskList, name, skip string) error {
	for _, l := range lists {
		if l.ID != skip && normalize(l.Name) == normalize(name) {
			return fmt.Errorf("%s already has a checklist %q (id %s): pick another name", cardScope(cr), l.Name, l.ID)
		}
	}
	return nil
}

// checklistPosition is the position that puts a checklist where place says
// among the card's checklists (see between), leaving out skip, the one being
// moved. With place empty it goes last.
func checklistPosition(cr *planka.CardResponse, lists []planka.TaskList, place ListPlace, skip string) (float64, error) {
	if place.After != "" && place.Before != "" {
		return 0, errors.New("give after or before, not both")
	}
	var ids []string
	var pos []float64
	for _, l := range lists {
		if l.ID != skip {
			ids = append(ids, l.ID)
			pos = append(pos, l.Position)
		}
	}
	ref := cmp.Or(place.After, place.Before)
	if ref == "" {
		return bottomPosition(pos), nil
	}
	anchor, err := exactChecklist(cr, lists, ref)
	if err != nil {
		return 0, err
	}
	if anchor.ID == skip {
		return 0, errors.New("a checklist cannot go after or before itself")
	}
	return between(pos, slices.Index(ids, anchor.ID), place.After != ""), nil
}
