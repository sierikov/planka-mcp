package service

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	defaultCommentsLimit = 20
	// plankaCommentsPage is how many comments Planka returns per request.
	plankaCommentsPage = 50
)

// Get returns a card in full: description, checklists, attachments, custom
// fields and its newest comments, at most commentsLimit of them (default 20).
// Tasks that stand for other cards carry a summary of the linked card.
func (c *Cards) Get(ctx context.Context, ref CardRef, commentsLimit int) (*Card, error) {
	id, snap, err := c.resolveID(ctx, ref)
	if err != nil {
		return nil, err
	}
	cr, err := c.cards.Card(ctx, id)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	if snap == nil {
		if snap, err = c.loadSnapshot(ctx, cr.Item.BoardID); err != nil {
			return nil, err
		}
	}
	comments, err := c.comments.Comments(ctx, id, "")
	if err != nil {
		return nil, err
	}

	item := cr.Item
	out := &Card{
		CardSummary:  cardSummary(snap.overlay(cardOverlay(cr)), id),
		Project:      snap.project,
		Type:         item.Type,
		Description:  deref(item.Description),
		Stopwatch:    c.stopwatch(item.Stopwatch),
		Attachments:  attachments(cr.Included.Attachments),
		CustomFields: c.customFields(ctx, cr, snap),
		Comments:     toComments(comments, snap, commentsLimit),
		Created:      item.CreatedAt,
	}
	out.TaskLists = c.taskLists(ctx, cr, snap)
	return out, nil
}

// Comments returns a card's comments, newest first, at most limit of them
// (default 20). A non-empty before is the ID of the oldest comment already
// seen: the page continues with the comments older than it. older reports
// that older comments exist or may: the limit cut some off, or Planka's page
// was full.
func (c *Cards) Comments(ctx context.Context, ref CardRef, before string, limit int) (comments []Comment, older bool, err error) {
	id, snap, err := c.resolveID(ctx, ref)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.comments.Comments(ctx, id, before)
	if err != nil {
		return nil, false, notFound("card", id, err)
	}
	shown := toComments(resp, snap, limit)
	return shown, len(shown) < len(resp.Items) || len(resp.Items) >= plankaCommentsPage, nil
}

// cardOverlay is what a card response adds to its board's snapshot.
func cardOverlay(cr *planka.CardResponse) planka.BoardIncluded {
	return planka.BoardIncluded{
		Cards:           []planka.Card{cr.Item},
		CardLabels:      cr.Included.CardLabels,
		CardMemberships: cr.Included.CardMemberships,
		TaskLists:       cr.Included.TaskLists,
		Tasks:           cr.Included.Tasks,
		Users:           cr.Included.Users,
	}
}

// stopwatch is the card's time tracker; a running one counts up to now.
func (c *Cards) stopwatch(sw *planka.Stopwatch) *Stopwatch {
	if sw == nil {
		return nil
	}
	total := time.Duration(sw.Total) * time.Second
	if sw.StartedAt != nil {
		total += max(0, c.now().Sub(*sw.StartedAt))
	}
	return &Stopwatch{Total: total, Running: sw.StartedAt != nil}
}

func attachments(in []planka.Attachment) []Attachment {
	out := make([]Attachment, 0, len(in))
	for _, a := range in {
		url, _ := a.Data["url"].(string)
		out = append(out, Attachment{Name: a.Name, URL: url})
	}
	return out
}

// customFields groups the card's field values by their group, the board's
// groups before the card's own, groups and fields in Planka's position order.
// A value's group is on the card or on the board, so both responses are looked
// in. A group made from a project's base group takes its name and fields from
// the base group, which only the projects listing carries: it is read only when
// a value needs it, and a value whose names cannot be read keeps its content.
func (c *Cards) customFields(ctx context.Context, cr *planka.CardResponse, snap *boardSnapshot) []CustomFieldGroup {
	values := cr.Included.CustomFieldValues
	groups := map[string]planka.CustomFieldGroup{}
	fields := map[string]planka.CustomField{}
	for _, inc := range []struct {
		groups []planka.CustomFieldGroup
		fields []planka.CustomField
	}{
		{snap.resp.Included.CustomFieldGroups, snap.resp.Included.CustomFields},
		{cr.Included.CustomFieldGroups, cr.Included.CustomFields},
	} {
		for _, g := range inc.groups {
			groups[g.ID] = g
		}
		for _, f := range inc.fields {
			fields[f.ID] = f
		}
	}

	baseGroups := map[string]string{}
	if needsBaseGroups(values, groups, fields) {
		if resp, err := c.catalog.projects.Projects(ctx); err == nil {
			for _, g := range resp.Included.BaseCustomFieldGroups {
				baseGroups[g.ID] = g.Name
			}
			for _, f := range resp.Included.CustomFields {
				fields[f.ID] = f
			}
		}
	}

	return groupFieldValues(values, groups, fields, baseGroups)
}

// groupFieldValues groups values by their group, the board's groups before the
// card's own, groups and fields in Planka's position order. baseGroups names
// the base groups by ID.
func groupFieldValues(
	values []planka.CustomFieldValue, groups map[string]planka.CustomFieldGroup,
	fields map[string]planka.CustomField, baseGroups map[string]string,
) []CustomFieldGroup {
	byGroup := map[string][]planka.CustomFieldValue{}
	var order []string
	for _, v := range values {
		if _, ok := byGroup[v.CustomFieldGroupID]; !ok {
			order = append(order, v.CustomFieldGroupID)
		}
		byGroup[v.CustomFieldGroupID] = append(byGroup[v.CustomFieldGroupID], v)
	}
	slices.SortStableFunc(order, func(a, b string) int {
		ga, gb := groups[a], groups[b]
		return cmp.Or(cmp.Compare(onCard(ga), onCard(gb)), cmp.Compare(ga.Position, gb.Position), cmp.Compare(a, b))
	})

	out := make([]CustomFieldGroup, 0, len(order))
	for _, id := range order {
		vs := byGroup[id]
		slices.SortStableFunc(vs, func(a, b planka.CustomFieldValue) int {
			fa, fb := fields[a.CustomFieldID], fields[b.CustomFieldID]
			return cmp.Or(cmp.Compare(fa.Position, fb.Position), cmp.Compare(a.CustomFieldID, b.CustomFieldID))
		})
		g := groups[id]
		name := deref(g.Name)
		if name == "" && g.BaseCustomFieldGroupID != nil {
			name = baseGroups[*g.BaseCustomFieldGroupID]
		}
		group := CustomFieldGroup{Name: name, Fields: make([]CustomField, len(vs))}
		for i, v := range vs {
			f := fields[v.CustomFieldID]
			group.Fields[i] = CustomField{Name: f.Name, Value: v.Content, ShowOnFrontOfCard: f.ShowOnFrontOfCard}
		}
		out = append(out, group)
	}
	return out
}

// onCard sorts a board's groups (0) before a card's own (1).
func onCard(g planka.CustomFieldGroup) int {
	if g.BoardID != nil {
		return 0
	}
	return 1
}

// needsBaseGroups reports whether a value's field is unknown, or its group takes
// its name from a base group.
func needsBaseGroups(values []planka.CustomFieldValue, groups map[string]planka.CustomFieldGroup, fields map[string]planka.CustomField) bool {
	for _, v := range values {
		if _, ok := fields[v.CustomFieldID]; !ok {
			return true
		}
		if g := groups[v.CustomFieldGroupID]; g.Name == nil && g.BaseCustomFieldGroupID != nil {
			return true
		}
	}
	return false
}

// toComments converts a page of comments, cut to limit. Authors are the users
// of the response and of snap, which may be nil.
func toComments(resp *planka.CommentsResponse, snap *boardSnapshot, limit int) []Comment {
	if limit <= 0 {
		limit = defaultCommentsLimit
	}
	authors := map[string]string{}
	if snap != nil {
		for id, u := range snap.users {
			authors[id] = userName(u)
		}
	}
	for _, u := range resp.Included.Users {
		authors[u.ID] = userName(u)
	}
	items := resp.Items[:min(len(resp.Items), limit)]
	out := make([]Comment, len(items))
	for i, cm := range items {
		out[i] = Comment{ID: cm.ID, Author: authors[cm.UserID], Text: cm.Text, Created: cm.CreatedAt}
	}
	return out
}

// taskLists converts the card's checklists. A task that stands for a card
// carries that card's summary.
func (c *Cards) taskLists(ctx context.Context, cr *planka.CardResponse, home *boardSnapshot) []TaskList {
	lk := &linker{cards: c, boards: map[string]*boardSnapshot{home.resp.Item.ID: home}}
	lists := slices.Clone(cr.Included.TaskLists)
	slices.SortStableFunc(lists, func(a, b planka.TaskList) int { return cmp.Compare(a.Position, b.Position) })

	out := make([]TaskList, len(lists))
	for i, tl := range lists {
		out[i] = toTaskList(tl, cr.Included.Tasks, func(cardID, name string) *CardSummary {
			return lk.summary(ctx, cardID, name)
		})
	}
	return out
}

// linker summarises the cards that tasks link to. It reads each foreign board
// at most once.
type linker struct {
	cards  *Cards
	boards map[string]*boardSnapshot // nil for a board that could not be read
}

// summary returns the linked card's summary. When the card cannot be read, it
// returns one that holds the ID and the task's name, so the task still shows.
func (l *linker) summary(ctx context.Context, id, taskName string) *CardSummary {
	fallback := &CardSummary{ID: id, Title: taskName}
	for _, snap := range l.boards {
		if snap == nil {
			continue
		}
		if _, ok := snap.cards[id]; ok {
			s := cardSummary(snap, id)
			return &s
		}
	}
	cr, err := l.cards.cards.Card(ctx, id)
	if err != nil {
		return fallback
	}
	boardID := cr.Item.BoardID
	snap, seen := l.boards[boardID]
	if !seen {
		snap, _ = l.cards.loadSnapshot(ctx, boardID)
		l.boards[boardID] = snap
	}
	if snap == nil {
		return fallback
	}
	s := cardSummary(snap.overlay(cardOverlay(cr)), id)
	return &s
}
