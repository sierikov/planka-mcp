package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// defaultTaskList names the checklist that Add uses when none is given.
const defaultTaskList = "Tasks"

// Tasks adds, changes and deletes the checklist items on cards. Reading them
// is (*Cards).Get.
type Tasks struct {
	cards *Cards
	api   TasksAPI
}

// NewTasks returns a Tasks service. It finds cards through cards and writes
// through api.
func NewTasks(cards *Cards, api TasksAPI) *Tasks {
	return &Tasks{cards: cards, api: api}
}

// TaskInput is one task to add: exactly one of Name and Link is set. Link
// makes the task stand for another card; it is a card ID, a card URL or a
// title, which is looked for on the parent card's board first and then on all
// boards. A link may not point at the parent card itself.
type TaskInput struct {
	Name, Link string
}

// Add appends tasks to a checklist of a card, at the bottom and in order, and
// returns the checklist as it is afterwards. The checklist is found by its
// name, exactly but ignoring case ("Tasks" when taskList is empty); a missing
// one is created at the bottom of the card's checklists. The card and every
// item are resolved before the first write, so a bad item changes nothing.
func (t *Tasks) Add(ctx context.Context, ref CardRef, taskList string, items []TaskInput) (*TaskList, error) {
	if err := validateInputs(items); err != nil {
		return nil, err
	}
	listName := strings.TrimSpace(taskList)
	if listName == "" {
		listName = defaultTaskList
	}
	id, snap, unlock, err := t.cards.lockCard(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer unlock()
	links, err := t.resolveLinks(ctx, id, snap.resp.Item.ID, items)
	if err != nil {
		return nil, err
	}
	cur, err := t.cards.cards.Card(ctx, id)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	list, err := findTaskList(cur.Included.TaskLists, listName)
	if err != nil {
		return nil, err
	}

	created := list == nil
	var taken []float64
	if created {
		req := planka.CreateTaskListRequest{Name: listName, Position: bottomPosition(taskListPositions(cur.Included.TaskLists))}
		if list, err = t.api.CreateTaskList(ctx, id, req); err != nil {
			return nil, err
		}
	} else {
		taken = taskPositions(cur.Included.Tasks, list.ID)
	}
	if err := t.addAll(ctx, list, created, bottomPosition(taken), items, links); err != nil {
		return nil, err
	}

	fresh, err := t.cards.cards.Card(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%d of %d items were added to card %s, but reading the card back failed: %w",
			len(items), len(items), id, err)
	}
	out := checklistOf(fresh, list, snap)
	if created {
		out.Created = true
		for _, tl := range cur.Included.TaskLists {
			out.Others = append(out.Others, tl.Name)
		}
	}
	return out, nil
}

// checklistOf is the checklist list as the card cr holds it. A task that
// links to a card on the board snap shows that card's summary; any other
// shows the ID and the task's name.
func checklistOf(cr *planka.CardResponse, list *planka.TaskList, snap *boardSnapshot) *TaskList {
	for _, tl := range cr.Included.TaskLists {
		if tl.ID == list.ID {
			out := toTaskList(tl, cr.Included.Tasks, boardView(cr, snap))
			return &out
		}
	}
	return &TaskList{ID: list.ID, Name: list.Name}
}

// boardView converts the tasks of the card cr with what its board snap knows:
// a linked card on the board gets its summary, one elsewhere its ID and the
// task's name.
func boardView(cr *planka.CardResponse, snap *boardSnapshot) taskView {
	return taskView{
		link: func(cardID, name string) *CardSummary {
			if _, ok := snap.cards[cardID]; ok {
				s := cardSummary(snap, cardID)
				return &s
			}
			return &CardSummary{ID: cardID, Title: name}
		},
		person: people(snap, cr.Included.Users),
	}
}

// addAll creates the tasks in list one after another, from position pos on.
// links holds the card each item links to, "" for an item with a name. A
// failure says how many were added; created says the list is new.
func (t *Tasks) addAll(
	ctx context.Context, list *planka.TaskList, created bool, pos float64, items []TaskInput, links []string,
) error {
	for i, it := range items {
		req := planka.CreateTaskRequest{Position: pos}
		if links[i] != "" {
			req.LinkedCardID = &links[i]
		} else {
			name := strings.TrimSpace(it.Name)
			req.Name = &name
		}
		if _, err := t.api.CreateTask(ctx, list.ID, req); err != nil {
			if i == 0 && !created {
				return err
			}
			note := fmt.Sprintf("%d of %d items were added", i, len(items))
			if created {
				note = fmt.Sprintf("task list %q was created; %s", list.Name, note)
			}
			return fmt.Errorf("%s, but item %d failed: %w", note, i+1, err)
		}
		pos += positionGap
	}
	return nil
}

// validateInputs checks that there are items and that each has a name or a
// link, not both and not neither.
func validateInputs(items []TaskInput) error {
	if len(items) == 0 {
		return errors.New("no items to add")
	}
	for i, it := range items {
		if (strings.TrimSpace(it.Name) == "") == (strings.TrimSpace(it.Link) == "") {
			return fmt.Errorf("item %d: give exactly one of name and link", i+1)
		}
	}
	return nil
}

// taskListPositions is the position of each of the checklists.
func taskListPositions(lists []planka.TaskList) []float64 {
	out := make([]float64, len(lists))
	for i, tl := range lists {
		out[i] = tl.Position
	}
	return out
}

// taskPositions is the position of each task in the checklist listID.
func taskPositions(tasks []planka.Task, listID string) []float64 {
	var out []float64
	for _, tk := range tasks {
		if tk.TaskListID == listID {
			out = append(out, tk.Position)
		}
	}
	return out
}

// resolveLinks returns the card ID each item links to, "" for an item that
// has a name. A title is looked for on the parent's board (by its ID, since
// board names repeat across projects), then on every board when that finds
// nothing or only a description match; several matches anywhere are an
// AmbiguousError, and a description match alone a DescriptionMatchError, as
// for every write. An ID or URL names
// a card without a search, so the card is read to make sure it exists. A link
// to the parent card itself is an error. Nothing is written here.
func (t *Tasks) resolveLinks(ctx context.Context, parentID, boardID string, items []TaskInput) ([]string, error) {
	out := make([]string, len(items))
	for i, it := range items {
		link := strings.TrimSpace(it.Link)
		if link == "" {
			continue
		}
		id, err := t.linkID(ctx, link, parentID, boardID)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		out[i] = id
	}
	return out, nil
}

// linkID resolves one link; see resolveLinks.
func (t *Tasks) linkID(ctx context.Context, link, parentID, boardID string) (string, error) {
	id, match, err := t.cards.resolveTarget(ctx, CardRef{Ref: link, Board: boardID})
	var (
		none *NoMatchError
		desc *DescriptionMatchError
	)
	if errors.As(err, &none) || errors.As(err, &desc) {
		id, match, err = t.cards.resolveTarget(ctx, CardRef{Ref: link})
	}
	if err != nil {
		return "", err
	}
	if id == parentID {
		return "", errors.New("a task cannot link to its own card")
	}
	if match == nil { // an ID or URL: nothing has checked it yet
		if _, err := t.cards.cards.Card(ctx, id); err != nil {
			return "", notFound("card", id, err)
		}
	}
	return id, nil
}

// findTaskList returns the checklist called name, ignoring case, or nil if
// there is none. No prefix matching: a missing checklist gets created, so a
// near miss must not pass for a hit. Two with the same name are ambiguous.
func findTaskList(lists []planka.TaskList, name string) (*planka.TaskList, error) {
	var found []planka.TaskList
	for _, tl := range lists {
		if normalize(tl.Name) == normalize(name) {
			found = append(found, tl)
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return &found[0], nil
	}
	cands := make([]Candidate, len(found))
	for i, tl := range found {
		cands[i] = Candidate{ID: tl.ID, Label: tl.Name}
	}
	return nil, &AmbiguousError{Kind: "task list", Query: name, Candidates: cands}
}

// taskView is what converting a task needs besides the task: link summarises
// the card a task stands for, person names a user by ID.
type taskView struct {
	link   func(cardID, name string) *CardSummary
	person func(userID string) string
}

// people names a user of the board snap, or one of extra (the users a card
// response carries). A user neither knows goes by ID.
func people(snap *boardSnapshot, extra []planka.User) func(string) string {
	return func(id string) string {
		if u, ok := snap.users[id]; ok {
			return userName(u)
		}
		for _, u := range extra {
			if u.ID == id {
				return userName(u)
			}
		}
		return id
	}
}

// tasksOf is the tasks of the checklist listID among all, by position.
func tasksOf(all []planka.Task, listID string) []planka.Task {
	own := slices.DeleteFunc(slices.Clone(all), func(tk planka.Task) bool { return tk.TaskListID != listID })
	slices.SortStableFunc(own, func(a, b planka.Task) int { return cmp.Compare(a.Position, b.Position) })
	return own
}

// toTaskList converts a checklist with its tasks, by position.
func toTaskList(tl planka.TaskList, all []planka.Task, v taskView) TaskList {
	out := TaskList{ID: tl.ID, Name: tl.Name, ShowOnFrontOfCard: tl.ShowOnFrontOfCard, HideCompletedTasks: tl.HideCompletedTasks}
	for _, tk := range tasksOf(all, tl.ID) {
		out.Tasks = append(out.Tasks, toTask(tk, v))
	}
	return out
}

// toTask converts a task. A task that stands for a card carries the link's
// summary of it, and is named after the card: Planka keeps the name the card
// had when it was linked.
func toTask(tk planka.Task, v taskView) Task {
	out := Task{ID: tk.ID, Name: deref(tk.Name), Completed: tk.IsCompleted}
	if tk.LinkedCardID != nil {
		out.Linked = v.link(*tk.LinkedCardID, out.Name)
		out.Name = cmp.Or(out.Linked.Title, out.Name)
	}
	if tk.AssigneeUserID != nil {
		out.Assignee = v.person(*tk.AssigneeUserID)
	}
	return out
}

// taskUpdateError explains why Planka refused to change a task. Planka answers
// 403 both to a user without rights on the board and to any change of a task
// that links a card other than moving it, and the answer does not say which,
// so the explanation names both. The 403 itself is not wrapped: the generic
// hint for it would only repeat half of this.
func taskUpdateError(id string, err error) error {
	if api, ok := errors.AsType[*planka.APIError](err); ok && api.Status == http.StatusForbidden {
		return fmt.Errorf("task %s: Planka refused the change (403). If the task links a card, it cannot be renamed or ticked: "+
			"Planka ticks it when the linked card moves to a closed list such as Done, and its name comes from that card. "+
			"Otherwise you lack rights on this board", id)
	}
	return notFound("task", id, err)
}
