package service

import (
	"cmp"
	"context"
	"maps"
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
// fields and its newest comments, at most commentsLimit of them. A
// commentsLimit of 0 skips the comments, and their read; the card still says
// how many it has. Tasks that stand for other cards carry a summary of the
// linked card.
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
	var comments []Comment
	if commentsLimit > 0 {
		page, err := c.comments.Comments(ctx, id, "")
		if err != nil {
			return nil, err
		}
		comments = toComments(page, snap, commentsLimit)
	}

	item := cr.Item
	out := &Card{
		CardSummary:  cardSummary(snap.overlay(cardOverlay(cr)), id),
		Project:      snap.project,
		Type:         item.Type,
		Description:  deref(item.Description),
		Stopwatch:    c.stopwatch(item.Stopwatch),
		Attachments:  attachments(cr.Included.Attachments, deref(item.CoverAttachmentID)),
		CustomFields: c.customFields(ctx, cr, snap),
		Comments:     comments,
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

// attachments converts a card's attachments; cover is the card's cover ID.
func attachments(in []planka.Attachment, cover string) []Attachment {
	out := make([]Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, toAttachment(a, cover))
	}
	return out
}

// customFields groups the card's field values by their group, groups and
// fields in Planka's order (see fieldCatalog). Each group also names its
// fields that have no value on the card. A value whose names cannot be read,
// because the projects listing failed, keeps its content.
func (c *Cards) customFields(ctx context.Context, cr *planka.CardResponse, snap *boardSnapshot) []CustomFieldGroup {
	cat, _ := c.fieldCatalog(ctx, cr, snap)
	return cat.groupValues(cr.Included.CustomFieldValues)
}

// fieldSlot is a custom field a card can have a value for: a field of one of
// the board's groups or of the card's own groups.
type fieldSlot struct {
	group planka.CustomFieldGroup
	field planka.CustomField
}

// fieldCatalog is what Planka says about the custom fields of one card: its
// groups and fields by ID, the names of the project base groups, and the
// card's fields in order.
type fieldCatalog struct {
	groups    map[string]planka.CustomFieldGroup
	fields    map[string]planka.CustomField
	baseNames map[string]string
	slots     []fieldSlot
}

// fieldCatalog collects the custom fields of the card cr on the board snap.
// The groups are the board's and the card's own: the board's first, each by
// position, and the fields of each by position. A group made from a project's
// base group takes its fields, and its name when it has none, from the base
// group, which only the projects listing carries: it is read only when a group
// or a value needs it. If that read fails, the catalog lacks those fields and
// the error says so.
func (c *Cards) fieldCatalog(ctx context.Context, cr *planka.CardResponse, snap *boardSnapshot) (*fieldCatalog, error) {
	cat := &fieldCatalog{groups: map[string]planka.CustomFieldGroup{}, fields: map[string]planka.CustomField{}, baseNames: map[string]string{}}
	for _, g := range slices.Concat(snap.resp.Included.CustomFieldGroups, cr.Included.CustomFieldGroups) {
		cat.groups[g.ID] = g
	}
	for _, f := range slices.Concat(snap.resp.Included.CustomFields, cr.Included.CustomFields) {
		cat.fields[f.ID] = f
	}
	var err error
	if cat.needsBaseGroups(cr.Included.CustomFieldValues) {
		var resp *planka.ProjectsResponse
		if resp, err = c.catalog.projects.Projects(ctx); err == nil {
			for _, g := range resp.Included.BaseCustomFieldGroups {
				cat.baseNames[g.ID] = g.Name
			}
			for _, f := range resp.Included.CustomFields {
				cat.fields[f.ID] = f
			}
		}
	}

	groups := slices.Collect(maps.Values(cat.groups))
	slices.SortFunc(groups, compareGroups)
	fields := slices.Collect(maps.Values(cat.fields))
	slices.SortFunc(fields, func(a, b planka.CustomField) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})
	for _, g := range groups {
		for _, f := range fields {
			if inGroup(f, g) {
				cat.slots = append(cat.slots, fieldSlot{group: g, field: f})
			}
		}
	}
	return cat, err
}

// needsBaseGroups reports whether a group takes its fields from a base group,
// or a value's field is unknown.
func (cat *fieldCatalog) needsBaseGroups(values []planka.CustomFieldValue) bool {
	for _, g := range cat.groups {
		if g.BaseCustomFieldGroupID != nil {
			return true
		}
	}
	for _, v := range values {
		if _, ok := cat.fields[v.CustomFieldID]; !ok {
			return true
		}
	}
	return false
}

// inGroup reports whether f is a field of g: of its base group, when g has one.
func inGroup(f planka.CustomField, g planka.CustomFieldGroup) bool {
	if g.BaseCustomFieldGroupID != nil {
		return f.BaseCustomFieldGroupID != nil && *f.BaseCustomFieldGroupID == *g.BaseCustomFieldGroupID
	}
	return f.CustomFieldGroupID == g.ID
}

// compareGroups orders groups as Planka shows them: the board's before the
// card's own, then by position.
func compareGroups(a, b planka.CustomFieldGroup) int {
	return cmp.Or(cmp.Compare(onCard(a), onCard(b)), cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
}

// groupName is a group's name, or its base group's.
func (cat *fieldCatalog) groupName(g planka.CustomFieldGroup) string {
	name := deref(g.Name)
	if name == "" && g.BaseCustomFieldGroupID != nil {
		name = cat.baseNames[*g.BaseCustomFieldGroupID]
	}
	return name
}

// slotName names a field as "Group / Field", or by the field alone when its
// group has no name.
func (cat *fieldCatalog) slotName(s fieldSlot) string {
	if g := cat.groupName(s.group); g != "" {
		return g + " / " + s.field.Name
	}
	return s.field.Name
}

// groupValues groups values by their group, in catalog order. A group lists
// its fields with a value, then those without as Unset; a value of a field the
// catalog lacks goes last in its group, with the name Planka gave, if any.
func (cat *fieldCatalog) groupValues(values []planka.CustomFieldValue) []CustomFieldGroup {
	byGroup := map[string]map[string]planka.CustomFieldValue{}
	for _, v := range values {
		if byGroup[v.CustomFieldGroupID] == nil {
			byGroup[v.CustomFieldGroupID] = map[string]planka.CustomFieldValue{}
		}
		byGroup[v.CustomFieldGroupID][v.CustomFieldID] = v
	}
	var order []string
	for _, s := range cat.slots {
		if !slices.Contains(order, s.group.ID) {
			order = append(order, s.group.ID)
		}
	}
	for id := range byGroup {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	slices.SortStableFunc(order, func(a, b string) int { return compareGroups(cat.groups[a], cat.groups[b]) })

	var out []CustomFieldGroup
	for _, id := range order {
		if group := cat.group(id, byGroup[id]); len(group.Fields) > 0 || len(group.Unset) > 0 {
			out = append(out, group)
		}
	}
	return out
}

// group is the group id with the values vs of its fields, by field ID.
func (cat *fieldCatalog) group(id string, vs map[string]planka.CustomFieldValue) CustomFieldGroup {
	group := CustomFieldGroup{Name: cat.groupName(cat.groups[id])}
	vs = maps.Clone(vs)
	for _, s := range cat.slots {
		if s.group.ID != id {
			continue
		}
		if v, ok := vs[s.field.ID]; ok {
			group.Fields = append(group.Fields, CustomField{Name: s.field.Name, Value: v.Content, ShowOnFrontOfCard: s.field.ShowOnFrontOfCard})
			delete(vs, s.field.ID)
		} else {
			group.Unset = append(group.Unset, s.field.Name)
		}
	}
	rest := slices.Collect(maps.Values(vs))
	slices.SortFunc(rest, func(a, b planka.CustomFieldValue) int {
		fa, fb := cat.fields[a.CustomFieldID], cat.fields[b.CustomFieldID]
		return cmp.Or(cmp.Compare(fa.Position, fb.Position), cmp.Compare(a.CustomFieldID, b.CustomFieldID))
	})
	for _, v := range rest {
		f := cat.fields[v.CustomFieldID]
		group.Fields = append(group.Fields, CustomField{Name: f.Name, Value: v.Content, ShowOnFrontOfCard: f.ShowOnFrontOfCard})
	}
	return group
}

// onCard sorts a board's groups (0) before a card's own (1).
func onCard(g planka.CustomFieldGroup) int {
	if g.BoardID != nil {
		return 0
	}
	return 1
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

	v := taskView{
		link:   func(cardID, name string) *CardSummary { return lk.summary(ctx, cardID, name) },
		person: people(home, cr.Included.Users),
	}
	out := make([]TaskList, len(lists))
	for i, tl := range lists {
		out[i] = toTaskList(tl, cr.Included.Tasks, v)
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
