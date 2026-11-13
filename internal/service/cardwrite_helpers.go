package service

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	// positionGap is Planka's POSITION_GAP, the distance between neighbours.
	positionGap = 65536
	// archivePosition is the position of a card sent to the archive.
	archivePosition = positionGap
	// defaultLabelColor colours the labels a write has to create.
	defaultLabelColor = "muddy-grey"
)

// topPosition is the position that puts a card before every one of existing:
// half the smallest, or the first slot of an empty list.
func topPosition(existing []float64) float64 {
	if len(existing) == 0 {
		return positionGap
	}
	return slices.Min(existing) / 2
}

// bottomPosition is the position that puts a card after every one of existing.
func bottomPosition(existing []float64) float64 {
	if len(existing) == 0 {
		return positionGap
	}
	return slices.Max(existing) + positionGap
}

// pickPosition turns "top" (or empty) and "bottom" into a position among existing.
func pickPosition(where string, existing []float64) (float64, error) {
	switch normalize(where) {
	case "", "top":
		return topPosition(existing), nil
	case "bottom":
		return bottomPosition(existing), nil
	}
	return 0, fmt.Errorf("unknown position %q: use top or bottom", where)
}

// listPositions returns the positions of the cards in a list, leaving out the
// card skip (the one being moved).
func listPositions(snap *boardSnapshot, listID, skip string) []float64 {
	var out []float64
	for _, card := range snap.resp.Included.Cards {
		if card.ListID == listID && card.ID != skip && card.Position != nil {
			out = append(out, *card.Position)
		}
	}
	return out
}

// firstOrNamedList is the named kanban list, or the board's first active list
// when no name is given.
func firstOrNamedList(snap *boardSnapshot, name string) (planka.List, error) {
	if strings.TrimSpace(name) != "" {
		return snap.kanbanListByName(name)
	}
	for _, l := range snap.kanbanLists() {
		if l.Type == "active" {
			return l, nil
		}
	}
	return planka.List{}, fmt.Errorf("%s has no active list", snap.scope())
}

// parseDue reads a due date: "2006-01-02" (midnight), "2006-01-02 15:04", both
// in loc, or RFC 3339, which carries its own zone.
func parseDue(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf(
		"invalid due date %q: use YYYY-MM-DD, YYYY-MM-DD HH:MM or RFC 3339 (2026-10-20T14:30:00+02:00)", s)
}

// optionalDue parses a due date, or returns nil for an empty s.
func optionalDue(s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	due, err := parseDue(s, time.Local)
	if err != nil {
		return nil, err
	}
	return &due, nil
}

// validatePatch checks the input that needs no API call and returns the card
// fields it sets: title, description, due date and whether it is done. The
// stopwatch needs the card and is added by withStopwatch.
func (c *Cards) validatePatch(in UpdateCard) (planka.UpdateCardRequest, error) {
	var req planka.UpdateCardRequest
	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return req, errors.New("card title is empty")
		}
		req.Name = planka.Some(*in.Title)
	}
	if in.Description != nil {
		req.Description = optionalText(*in.Description)
	}
	if in.Due != nil {
		if strings.TrimSpace(*in.Due) == "" {
			req.DueDate = planka.Null[time.Time]()
		} else {
			due, err := parseDue(*in.Due, time.Local)
			if err != nil {
				return req, err
			}
			req.DueDate = planka.Some(due)
		}
	}
	if in.DueCompleted != nil {
		req.IsDueCompleted = planka.Some(*in.DueCompleted)
	}
	if !slices.Contains([]string{"", "start", "stop", "reset"}, normalize(in.Stopwatch)) {
		return req, fmt.Errorf("unknown stopwatch action %q: use start, stop or reset", in.Stopwatch)
	}
	return req, nil
}

// optionalText sends s, or null for an empty s: Planka rejects "".
func optionalText(s string) planka.Optional[string] {
	if s == "" {
		return planka.Null[string]()
	}
	return planka.Some(s)
}

// withStopwatch adds the stopwatch action to req. Starting a running
// stopwatch and stopping a stopped one change nothing.
func (c *Cards) withStopwatch(req planka.UpdateCardRequest, action string, prev *planka.Stopwatch) (planka.UpdateCardRequest, error) {
	var running bool
	var total int64
	if prev != nil {
		running, total = prev.StartedAt != nil, prev.Total
	}
	switch normalize(action) {
	case "start":
		if !running {
			now := c.now()
			req.Stopwatch = planka.Some(planka.Stopwatch{StartedAt: &now, Total: total})
		}
	case "stop":
		if running {
			elapsed := max(0, int64(c.now().Sub(*prev.StartedAt)/time.Second))
			req.Stopwatch = planka.Some(planka.Stopwatch{Total: total + elapsed})
		}
	case "reset":
		req.Stopwatch = planka.Null[planka.Stopwatch]()
	}
	return req, nil
}
