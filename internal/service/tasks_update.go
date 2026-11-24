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

// TaskUpdate changes one task, found by ID; a nil or empty field is left
// alone. Checklist moves the task to another checklist of the same card, by
// ID or exact name. Position ("top" or "bottom"), After or Before (a task ID)
// places it in its checklist; a task moved without one goes to the bottom.
// Assignee is a board member's name, username or ID, "me", or "" to remove the
// assignee.
type TaskUpdate struct {
	ID                      string
	Completed               *bool
	Name                    *string
	Checklist               string
	Position, After, Before string
	Assignee                *string
}

// placesOrAssigns says the update moves, reorders or assigns the task, which
// needs its card.
func (u TaskUpdate) placesOrAssigns() bool {
	return u.Checklist != "" || u.Position != "" || u.After != "" || u.Before != "" || u.Assignee != nil
}

// Update changes tasks in order and returns them as Planka answered. Moving,
// reordering and assigning need card, the card the tasks are on: Planka finds
// a task's checklists and board only through its card. With card, the board's
// write lock is held, every move is planned on the card as the earlier items
// leave it, and the result names each task's checklist. Every item is checked
// before the first write.
func (t *Tasks) Update(ctx context.Context, card CardRef, items []TaskUpdate) ([]Task, error) {
	ids, err := checkTaskUpdates(items, strings.TrimSpace(card.Ref) != "")
	if err != nil {
		return nil, err
	}
	reqs := make([]planka.UpdateTaskRequest, len(items))
	if strings.TrimSpace(card.Ref) == "" {
		for i, it := range items {
			reqs[i] = taskFields(it)
		}
		got, err := t.applyTaskUpdates(ctx, ids, reqs)
		if err != nil {
			return nil, err
		}
		plain := taskView{
			link:   func(cardID, name string) *CardSummary { return &CardSummary{ID: cardID, Title: name} },
			person: func(id string) string { return id },
		}
		out := make([]Task, len(got))
		for i, g := range got {
			out[i] = toTask(g, plain)
		}
		return out, nil
	}

	_, snap, cr, unlock, err := t.lockChecklists(ctx, card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	plan := newTaskPlan(cr, snap, t.cards.users)
	for i, it := range items {
		if reqs[i], err = plan.request(ctx, i, ids[i], it); err != nil {
			return nil, err
		}
	}
	got, err := t.applyTaskUpdates(ctx, ids, reqs)
	if err != nil {
		return nil, err
	}
	view := boardView(cr, snap)
	out := make([]Task, len(got))
	for i, g := range got {
		out[i] = toTask(g, view)
		out[i].Checklist = plan.checklistName(g.TaskListID)
	}
	return out, nil
}

// checkTaskUpdates checks the items without reading anything and returns
// their trimmed IDs. withCard says a card was given.
func checkTaskUpdates(items []TaskUpdate, withCard bool) ([]string, error) {
	if len(items) == 0 {
		return nil, errors.New("no items to update")
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = strings.TrimSpace(it.ID)
		if ids[i] == "" {
			return nil, fmt.Errorf("item %d: task ID is empty", i+1)
		}
		if err := checkTaskUpdate(it, withCard); err != nil {
			return nil, fmt.Errorf("item %d (%s): %w", i+1, ids[i], err)
		}
	}
	return ids, nil
}

// checkTaskUpdate checks one item of checkTaskUpdates.
func checkTaskUpdate(it TaskUpdate, withCard bool) error {
	places := 0
	for _, p := range []string{it.Position, it.After, it.Before} {
		if p != "" {
			places++
		}
	}
	switch {
	case it.Completed == nil && it.Name == nil && !it.placesOrAssigns():
		return errors.New("nothing to change, give completed, name, checklist, position, after, before or assignee")
	case it.Name != nil && strings.TrimSpace(*it.Name) == "":
		return errors.New("task name is empty")
	case places > 1:
		return errors.New("give one of position, after and before")
	case it.Position != "" && !slices.Contains(Positions, it.Position):
		return fmt.Errorf("unknown position %q: use %s", it.Position, orList(Positions))
	case it.placesOrAssigns() && !withCard:
		return errors.New("moving, reordering or assigning a task needs card, the card the task is on")
	}
	return nil
}

// taskFields is the part of an update that needs no card: the name and the tick.
func taskFields(it TaskUpdate) planka.UpdateTaskRequest {
	req := planka.UpdateTaskRequest{IsCompleted: it.Completed}
	if it.Name != nil {
		name := strings.TrimSpace(*it.Name)
		req.Name = &name
	}
	return req
}

// applyTaskUpdates sends the updates in order. A failure after the first says
// which tasks were already changed.
func (t *Tasks) applyTaskUpdates(ctx context.Context, ids []string, reqs []planka.UpdateTaskRequest) ([]planka.Task, error) {
	out := make([]planka.Task, 0, len(ids))
	for i, id := range ids {
		got, err := t.api.UpdateTask(ctx, id, reqs[i])
		if err != nil {
			if i == 0 {
				return nil, taskUpdateError(id, err)
			}
			return nil, fmt.Errorf("tasks were partly updated (applied: %s), but item %d (%s) failed: %w",
				strings.Join(ids[:i], ", "), i+1, id, taskUpdateError(id, err))
		}
		out = append(out, *got)
	}
	return out, nil
}

// taskPlan plans task updates on one card: the card's checklists, and its
// tasks as the updates planned so far leave them.
type taskPlan struct {
	cr    *planka.CardResponse
	snap  *boardSnapshot
	lists []planka.TaskList
	tasks map[string]planka.Task
	users UsersAPI
	me    string
}

func newTaskPlan(cr *planka.CardResponse, snap *boardSnapshot, users UsersAPI) *taskPlan {
	p := &taskPlan{cr: cr, snap: snap, lists: sortedChecklists(cr), tasks: map[string]planka.Task{}, users: users}
	for _, tk := range cr.Included.Tasks {
		p.tasks[tk.ID] = tk
	}
	return p
}

// request is the PATCH for item i, the task id. A move or a placement is
// recorded, so the items after it plan from where it left the task.
func (p *taskPlan) request(ctx context.Context, i int, id string, it TaskUpdate) (planka.UpdateTaskRequest, error) {
	tk, ok := p.tasks[id]
	if !ok {
		return planka.UpdateTaskRequest{}, fmt.Errorf("item %d: task %s is not on %s", i+1, id, cardScope(p.cr))
	}
	if tk.LinkedCardID != nil && (it.Completed != nil || it.Name != nil || it.Assignee != nil) {
		return planka.UpdateTaskRequest{}, fmt.Errorf("item %d (%s): the task links a card, so it can only be moved or reordered: "+
			"Planka ticks it when the linked card moves to a closed list such as Done, and its name comes from that card", i+1, id)
	}
	req := taskFields(it)
	if err := p.move(&req, i, it, tk); err != nil {
		return req, err
	}
	if it.Assignee != nil {
		assignee, err := p.assignee(ctx, *it.Assignee)
		if err != nil {
			return req, fmt.Errorf("item %d (%s): %w", i+1, id, err)
		}
		req.AssigneeUserID = assignee
	}
	return req, nil
}

// move sets the checklist and position item i gives the task tk, and records
// them, so the items after it plan from there. An item that neither moves nor
// places the task leaves req alone.
func (p *taskPlan) move(req *planka.UpdateTaskRequest, i int, it TaskUpdate, tk planka.Task) error {
	listID := tk.TaskListID
	if it.Checklist != "" {
		l, err := exactChecklist(p.cr, p.lists, it.Checklist)
		if err != nil {
			return fmt.Errorf("item %d (%s): %w", i+1, tk.ID, err)
		}
		listID = l.ID
	}
	if listID == tk.TaskListID && it.Position == "" && it.After == "" && it.Before == "" {
		return nil
	}
	pos, err := p.position(i, tk.ID, it, listID)
	if err != nil {
		return err
	}
	req.Position = &pos
	if listID != tk.TaskListID {
		req.TaskListID = &listID
	}
	tk.TaskListID, tk.Position = listID, pos
	p.tasks[tk.ID] = tk
	return nil
}

// position is where item i puts the task id in the checklist listID: right
// after or before another task of it, at its top, or at its bottom.
func (p *taskPlan) position(i int, id string, it TaskUpdate, listID string) (float64, error) {
	var ids []string
	var pos []float64
	for _, tk := range tasksOf(p.current(), listID) {
		if tk.ID != id {
			ids = append(ids, tk.ID)
			pos = append(pos, tk.Position)
		}
	}
	anchor := cmp.Or(strings.TrimSpace(it.After), strings.TrimSpace(it.Before))
	switch {
	case anchor == id:
		return 0, fmt.Errorf("item %d (%s): a task cannot go after or before itself", i+1, id)
	case anchor != "":
		at := slices.Index(ids, anchor)
		if at < 0 {
			return 0, fmt.Errorf("item %d (%s): task %s is not in checklist %q", i+1, id, anchor, p.checklistName(listID))
		}
		return between(pos, at, it.After != ""), nil
	case it.Position == "top":
		return topPosition(pos), nil
	}
	return bottomPosition(pos), nil
}

// assignee is the assignee field for name: null for "", else the board member
// it names.
func (p *taskPlan) assignee(ctx context.Context, name string) (planka.Optional[string], error) {
	if strings.TrimSpace(name) == "" {
		return planka.Null[string](), nil
	}
	if normalize(name) == "me" && p.me == "" {
		me, err := p.users.Me(ctx)
		if err != nil {
			return planka.Optional[string]{}, err
		}
		p.me = me.ID
	}
	u, err := p.snap.userByName(name, p.me)
	if err != nil {
		return planka.Optional[string]{}, err
	}
	return planka.Some(u.ID), nil
}

// current is the card's tasks as the planned updates leave them, in the
// card's order, so equal positions keep a stable order.
func (p *taskPlan) current() []planka.Task {
	out := make([]planka.Task, len(p.cr.Included.Tasks))
	for i, tk := range p.cr.Included.Tasks {
		out[i] = p.tasks[tk.ID]
	}
	return out
}

// checklistName is the name of the card's checklist id.
func (p *taskPlan) checklistName(id string) string {
	for _, l := range p.lists {
		if l.ID == id {
			return l.Name
		}
	}
	return ""
}

// Delete deletes tasks, found by their IDs, in order. A failure after the
// first says which tasks were already deleted.
func (t *Tasks) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return errors.New("no tasks to delete")
	}
	clean := make([]string, len(ids))
	for i, id := range ids {
		clean[i] = strings.TrimSpace(id)
		switch {
		case clean[i] == "":
			return fmt.Errorf("item %d: task ID is empty", i+1)
		case slices.Contains(clean[:i], clean[i]):
			return fmt.Errorf("task %s is listed twice", clean[i])
		}
	}
	for i, id := range clean {
		if err := t.api.DeleteTask(ctx, id); err != nil {
			if i == 0 {
				return notFound("task", id, err)
			}
			return fmt.Errorf("tasks were partly deleted (deleted: %s), but task %s failed: %w",
				strings.Join(clean[:i], ", "), id, notFound("task", id, err))
		}
	}
	return nil
}
