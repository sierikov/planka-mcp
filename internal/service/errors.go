package service

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound reports that Planka has no such record, or the user cannot see it.
var ErrNotFound = errors.New("not found")

// Candidate is one of several records a name could mean. ID is empty when the
// matcher only knew names.
type Candidate struct {
	ID, Label string
}

// AmbiguousError reports a name that matches several records equally well.
// More counts the matches left out of Candidates.
type AmbiguousError struct {
	Kind, Query string
	Candidates  []Candidate
	More        int
}

// Error lists the candidates and says how to retry. When each has an ID, the
// retry goes by ID: the name alone is what was ambiguous.
func (e *AmbiguousError) Error() string {
	parts := make([]string, len(e.Candidates))
	retry := "retry with the id of the one you mean"
	for i, c := range e.Candidates {
		parts[i] = c.Label
		if c.ID != "" {
			parts[i] += " (id " + c.ID + ")"
		} else {
			retry = "retry with one of them"
		}
	}
	if e.More > 0 {
		parts = append(parts, fmt.Sprintf("and %d more not shown (narrow with board or more words)", e.More))
	}
	return fmt.Sprintf("%q matches several %ss: %s; %s", e.Query, e.Kind, strings.Join(parts, "; "), retry)
}

// DescriptionMatchError reports a write whose card reference matched one card,
// but only in its description. A write needs the card's title or ID: a word in
// some description is too weak a reason to change or delete a card.
type DescriptionMatchError struct {
	Query string
	Card  Candidate
}

func (e *DescriptionMatchError) Error() string {
	return fmt.Sprintf("%q matches only the description of card %s (id %s); to change that card, retry with its id",
		e.Query, e.Card.Label, e.Card.ID)
}

// UnknownNameError reports a name that matches nothing; Valid lists the names
// that would have worked, so the caller can correct itself.
type UnknownNameError struct {
	Kind, Name, Scope string
	Valid             []string
	// Hint, when set, says what else the caller can do.
	Hint string
}

func (e *UnknownNameError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "no %s %q", e.Kind, e.Name)
	if e.Scope != "" {
		b.WriteString(" on " + e.Scope)
	}
	valid := "(none)"
	if len(e.Valid) > 0 {
		valid = strings.Join(e.Valid, ", ")
	}
	fmt.Fprintf(&b, "; %ss: %s", e.Kind, valid)
	if e.Hint != "" {
		b.WriteString("; " + e.Hint)
	}
	return b.String()
}

// NoMatchError reports a card search that found nothing. ArchiveHint says the
// archive was not searched and could be. Its text names only search_cards'
// include_archived, because most tools that look a card up do not take it: a
// retry with it there would be refused.
type NoMatchError struct {
	Kind, Query, Scope string
	ArchiveHint        bool
}

func (e *NoMatchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "no %s matching %q", e.Kind, e.Query)
	if e.Scope != "" {
		b.WriteString(" on " + e.Scope)
	}
	b.WriteString("; every word must be in the title or in the description: try fewer or other words")
	if e.ArchiveHint {
		b.WriteString("; archived cards were not searched: find one with search_cards include_archived=true, then use its id")
	}
	return b.String()
}

// orList joins values as "a, b or c", for the error that lists them.
func orList(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}
	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}
