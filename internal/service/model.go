// Package service holds the use cases of the Planka MCP server. A service
// consumes narrow interfaces over the Planka client and returns the domain
// types of this file: never formatted strings, never MCP types.
package service

import "time"

// ProjectBoards is one project with the boards the user can see in it.
type ProjectBoards struct {
	Project string
	Boards  []BoardRef
}

// BoardRef identifies a board and the project it belongs to.
type BoardRef struct {
	ID, Name, Project string
}

// Board is a kanban board: its lists in display order, each with its cards.
type Board struct {
	ID, Name, Project string
	Lists             []List
	Labels            []string
	Members           []string
}

// List is a kanban column. Closed lists hold cards that are finished or dropped.
type List struct {
	ID, Name, Color string
	Closed          bool
	Cards           []CardSummary
}

// CardSummary is the short form of a card. It never carries the description.
type CardSummary struct {
	ID, Title, Board, List string
	Labels, Assignees      []string
	Due                    *time.Time
	DueCompleted           bool
	TasksDone, TasksTotal  int
	CommentsTotal          int
}

// Card is the full form of a card.
type Card struct {
	CardSummary
	Project, Type, Description string
	Stopwatch                  *Stopwatch
	TaskLists                  []TaskList
	Attachments                []Attachment
	CustomFields               []CustomFieldGroup
	Comments                   []Comment
	Created                    time.Time
}

// Stopwatch is a card's time tracker.
type Stopwatch struct {
	Total   time.Duration
	Running bool
}

// TaskList is a checklist on a card. ShowOnFrontOfCard and
// HideCompletedTasks are how Planka displays it (by default: shown on the
// card, ticked tasks shown).
type TaskList struct {
	ID, Name                              string
	ShowOnFrontOfCard, HideCompletedTasks bool
	Tasks                                 []Task
	// Created says Tasks.Add made this checklist; Others then names the
	// card's other checklists, in case the new name was a typo of one.
	Created bool
	Others  []string
}

// Task is a checklist item. Linked is set when the task stands for another
// card, and Assignee names the person the task is assigned to. Checklist names
// the checklist the task is in, where a result needs it (update_tasks).
type Task struct {
	ID, Name  string
	Completed bool
	Linked    *CardSummary
	Assignee  string
	Checklist string
}

// Attachment is a file or link attached to a card. Type is "file" or "link".
// URL is the link, or the file's download address, which only someone signed
// in to Planka can open. Size is a file's size in bytes. Cover marks the image
// shown on the card's front.
type Attachment struct {
	ID, Name, Type, URL string
	Size                int64
	Cover               bool
}

// CustomFieldGroup is a group of custom fields that have values on a card.
// Name is empty when Planka's answer did not name it.
type CustomFieldGroup struct {
	Name   string
	Fields []CustomField
}

// CustomField is a custom field's value on a card, verbatim: Planka stores
// custom fields as plain text. ShowOnFrontOfCard is set for the fields the
// board shows on the front of its cards.
type CustomField struct {
	Name, Value       string
	ShowOnFrontOfCard bool
}

// Comment is a comment on a card.
type Comment struct {
	ID, Author, Text string
	Created          time.Time
}
