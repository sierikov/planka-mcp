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
	ID, Name string
	Closed   bool
	Cards    []CardSummary
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

// TaskList is a checklist on a card.
type TaskList struct {
	ID, Name string
	Tasks    []Task
}

// Task is a checklist item. Linked is set when the task stands for another card.
type Task struct {
	ID, Name  string
	Completed bool
	Linked    *CardSummary
}

// Attachment is a file or link attached to a card.
type Attachment struct {
	Name, URL string
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
