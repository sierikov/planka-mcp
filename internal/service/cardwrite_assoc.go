package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// assocInput names the labels and assignees a write adds and removes. onCard,
// when set, is a snapshot that holds the card, so that what is already there
// is not added again. createLabels lets added label names the board lacks be
// created.
type assocInput struct {
	addLabels, removeLabels []string
	createLabels            bool
	addUsers, removeUsers   []string
	onCard                  *boardSnapshot
}

// labelAdd is a label to attach: an existing one, or a name to create first.
type labelAdd struct {
	label  planka.Label
	create string
}

// assocPlan is the label and member calls a write makes, in order.
type assocPlan struct {
	labelAdds    []labelAdd
	labelRemoves []planka.Label
	userAdds     []planka.User
	userRemoves  []planka.User
}

// planAssociations resolves every label and assignee name on the board and
// drops what would change nothing. It writes nothing. cardID is empty for a
// card that does not exist yet.
func (c *Cards) planAssociations(ctx context.Context, snap *boardSnapshot, cardID string, in assocInput) (assocPlan, error) {
	var haveLabels, haveUsers []string
	if in.onCard != nil {
		haveLabels, haveUsers = in.onCard.cardLabel[cardID], in.onCard.cardUser[cardID]
	}
	adds, removes, err := planLabels(snap, haveLabels, in.addLabels, in.removeLabels, in.createLabels)
	if err != nil {
		return assocPlan{}, err
	}
	p := assocPlan{labelAdds: adds, labelRemoves: removes}

	var me *planka.User
	userAdds, err := c.resolveUsers(ctx, snap, in.addUsers, &me)
	if err != nil {
		return assocPlan{}, err
	}
	userRemoves, err := c.resolveUsers(ctx, snap, in.removeUsers, &me)
	if err != nil {
		return assocPlan{}, err
	}
	for _, u := range userAdds {
		if !slices.Contains(haveUsers, u.ID) {
			p.userAdds = append(p.userAdds, u)
		}
	}
	for _, u := range userRemoves {
		if slices.Contains(haveUsers, u.ID) {
			p.userRemoves = append(p.userRemoves, u)
		}
	}
	return p, nil
}

// planLabels resolves label names on the board. A label is added only on its
// ID or an exact match of its display name (case and surrounding space
// ignored). A name with no exact match is created when create is set, once
// however often it is named; otherwise it is an UnknownNameError, so that a
// typo never becomes a new label. Removal is looser: exact, then prefix,
// because it can only name a label that exists. A label already on the card
// is not added again and one not on it is not removed. have holds the IDs of
// the labels on the card.
func planLabels(snap *boardSnapshot, have, add, remove []string, create bool) (adds []labelAdd, removes []planka.Label, err error) {
	if adds, err = planLabelAdds(snap, have, add, create); err != nil {
		return nil, nil, err
	}
	for _, name := range remove {
		l, err := snap.labelByName(name)
		if err != nil {
			return nil, nil, err
		}
		if slices.Contains(have, l.ID) && !slices.ContainsFunc(removes, func(r planka.Label) bool { return r.ID == l.ID }) {
			removes = append(removes, l)
		}
	}
	return adds, removes, nil
}

// planLabelAdds is the adding half of planLabels.
func planLabelAdds(snap *boardSnapshot, have, add []string, create bool) ([]labelAdd, error) {
	var adds []labelAdd
	for _, name := range add {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("label name is empty")
		}
		l, found, err := exactLabel(snap, name)
		switch {
		case err != nil:
			return nil, err
		case !found && !create:
			return nil, unknownLabel(snap, strings.TrimSpace(name))
		case !found:
			name = strings.TrimSpace(name)
			if !slices.ContainsFunc(adds, func(a labelAdd) bool { return normalize(a.create) == normalize(name) }) {
				adds = append(adds, labelAdd{create: name})
			}
		case !slices.Contains(have, l.ID) && !slices.ContainsFunc(adds, func(a labelAdd) bool { return a.label.ID == l.ID }):
			adds = append(adds, labelAdd{label: l})
		}
	}
	return adds, nil
}

// exactLabel finds the label whose ID is name, or else whose display name
// equals name. Two labels of that name are an AmbiguousError that lists their
// IDs.
func exactLabel(snap *boardSnapshot, name string) (planka.Label, bool, error) {
	labels := snap.sortedLabels()
	if l, ok := byID(name, labels, labelID); ok {
		return l, true, nil
	}
	var found []planka.Label
	for _, l := range labels {
		if normalize(labelName(l)) == normalize(name) {
			found = append(found, l)
		}
	}
	switch len(found) {
	case 0:
		return planka.Label{}, false, nil
	case 1:
		return found[0], true, nil
	}
	cands := make([]Candidate, len(found))
	for i, l := range found {
		cands[i] = Candidate{ID: l.ID, Label: labelName(l)}
	}
	return planka.Label{}, false, &AmbiguousError{Kind: "label", Query: name, Candidates: cands}
}

// members is the board's view restricted to the users who are members of it.
// The users the board response lists also include, for example, the creators of
// cards who have since left the board.
func (s *boardSnapshot) members() *boardSnapshot {
	r := *s.resp
	r.Included.Users = nil
	for _, m := range s.resp.Included.BoardMemberships {
		if u, ok := s.users[m.UserID]; ok {
			r.Included.Users = append(r.Included.Users, u)
		}
	}
	return newSnapshot(&r, s.project)
}

// resolveUsers finds the board members the names stand for, each once. "me"
// is the user of the API key, read once into me. A user who is not a member of
// the board is an error that lists the members.
func (c *Cards) resolveUsers(ctx context.Context, snap *boardSnapshot, names []string, me **planka.User) ([]planka.User, error) {
	if len(names) == 0 {
		return nil, nil
	}
	board := snap.members()
	var out []planka.User
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("assignee name is empty")
		}
		meID := ""
		if normalize(name) == "me" {
			if *me == nil {
				var err error
				if *me, err = c.users.Me(ctx); err != nil {
					return nil, err
				}
			}
			meID = (*me).ID
			if _, ok := board.users[meID]; !ok {
				members := make([]string, len(board.resp.Included.Users))
				for i, m := range board.resp.Included.Users {
					members[i] = userName(m)
				}
				return nil, fmt.Errorf("you (%s) are not a member of %s; members: %s",
					userName(**me), snap.scope(), strings.Join(members, ", "))
			}
		}
		u, err := board.userByName(name, meID)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(out, func(have planka.User) bool { return have.ID == u.ID }) {
			out = append(out, u)
		}
	}
	return out, nil
}

// applyAssociations makes the calls of the plan: it creates the missing
// labels, then attaches labels, detaches labels, assigns users and unassigns
// users. It returns the names of the labels it created.
func (c *Cards) applyAssociations(ctx context.Context, snap *boardSnapshot, cardID string, p assocPlan) ([]string, error) {
	var created []string
	var positions []float64
	for _, l := range snap.resp.Included.Labels {
		positions = append(positions, l.Position)
	}
	for i, add := range p.labelAdds {
		if add.create == "" {
			continue
		}
		pos := bottomPosition(positions)
		l, err := c.labels.CreateLabel(ctx, snap.resp.Item.ID, add.create, defaultLabelColor, pos)
		if err != nil {
			return created, fmt.Errorf("create label %q: %w", add.create, err)
		}
		positions = append(positions, pos)
		created = append(created, add.create)
		p.labelAdds[i].label = *l
	}
	for _, add := range p.labelAdds {
		if err := c.labels.AddCardLabel(ctx, cardID, add.label.ID); err != nil {
			return created, fmt.Errorf("add label %q: %w", labelName(add.label), err)
		}
	}
	for _, l := range p.labelRemoves {
		if err := c.labels.RemoveCardLabel(ctx, cardID, l.ID); err != nil {
			return created, fmt.Errorf("remove label %q: %w", labelName(l), err)
		}
	}
	for _, u := range p.userAdds {
		if err := c.members.AddCardMember(ctx, cardID, u.ID); err != nil {
			return created, fmt.Errorf("assign %q: %w", userName(u), err)
		}
	}
	for _, u := range p.userRemoves {
		if err := c.members.RemoveCardMember(ctx, cardID, u.ID); err != nil {
			return created, fmt.Errorf("unassign %q: %w", userName(u), err)
		}
	}
	return created, nil
}

// appliedNote says, for an error message, what a write had already done: " (fields
// updated; labels created: a, b)", or "" when it had done nothing.
func appliedNote(fields bool, created []string) string {
	var parts []string
	if fields {
		parts = append(parts, "fields updated")
	}
	if len(created) > 0 {
		parts = append(parts, "labels created: "+strings.Join(created, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// unknownLabel reports a label name the board lacks, listing the board's
// labels and how to create it.
func unknownLabel(snap *boardSnapshot, name string) error {
	labels := snap.sortedLabels()
	valid := make([]string, len(labels))
	for i, l := range labels {
		valid[i] = labelName(l)
	}
	return &UnknownNameError{Kind: "label", Name: name, Scope: snap.scope(), Valid: valid, Hint: "set create_labels to create it"}
}
