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
type AmbiguousError struct {
	Kind, Query string
	Candidates  []Candidate
}

func (e *AmbiguousError) Error() string {
	parts := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		parts[i] = c.Label
		if c.ID != "" {
			parts[i] += " (id " + c.ID + ")"
		}
	}
	return fmt.Sprintf("%q matches several %ss: %s", e.Query, e.Kind, strings.Join(parts, ", "))
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

// NoMatchError reports a search that found nothing. ArchiveHint says the
// archive was not searched and could be.
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
	if e.ArchiveHint {
		b.WriteString("; archived cards were not searched, set include_archived to search the archive")
	}
	return b.String()
}
