package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

// The size fixtures model a long-lived board: a few hundred cards, most of them
// in closed lists, with realistic title lengths, IDs and metadata.

// sizeID returns a 19-digit ID like Planka's.
func sizeID(n int) string { return fmt.Sprintf("18990000000000%05d", n) }

// sizeText returns n characters of word-like text.
func sizeText(n int) string {
	const words = "update the release workflow and check the board labels before moving cards "
	return strings.Repeat(words, n/len(words)+1)[:n]
}

func sizeCard(n int, list string) service.CardSummary {
	c := service.CardSummary{
		ID:        sizeID(n),
		Title:     fmt.Sprintf("#%d: %s", n, sizeText(60)),
		Board:     "Planka MCP",
		List:      list,
		Labels:    []string{"feature", "panel-review"},
		Assignees: []string{"Artem Sierikov"},
	}
	if n%3 == 0 {
		due := time.Date(2026, 11, 2+n%20, 0, 0, 0, 0, time.Local)
		c.Due = &due
	}
	if n%2 == 0 {
		c.TasksDone, c.TasksTotal = n%7, 7
	}
	if n%4 == 0 {
		c.CommentsTotal = n % 9
	}
	return c
}

// sizeBoard fills each list with as many cards as it has room for.
func sizeBoard(lists []service.List) *service.Board {
	n := 0
	for i := range lists {
		count := len(lists[i].Cards)
		lists[i].Cards = make([]service.CardSummary, count)
		for j := range count {
			n++
			lists[i].Cards[j] = sizeCard(n, lists[i].Name)
		}
	}
	return &service.Board{
		ID: sizeID(0), Name: "Planka MCP", Project: "Projects", Lists: lists,
		Labels:  []string{"research", "feature", "release", "mcprush", "idea", "bug", "high", "panel-review", "refactor", "docs"},
		Members: []string{"Artem Sierikov", "Second Member", "Third Member"},
	}
}

func sizeLists(open, progress, done, dropped int) []service.List {
	return []service.List{
		{Name: "Open", Cards: make([]service.CardSummary, open)},
		{Name: "In progress", Cards: make([]service.CardSummary, progress)},
		{Name: "Ready for release"},
		{Name: "Done", Closed: true, Cards: make([]service.CardSummary, done)},
		{Name: "Dropped", Closed: true, Cards: make([]service.CardSummary, dropped)},
	}
}

func sizeComments(n, textLen int) []service.Comment {
	out := make([]service.Comment, n)
	for i := range out {
		out[i] = service.Comment{ID: sizeID(50000 + i), Author: "Artem Sierikov", Text: sizeText(textLen), Created: time.Date(2026, 11, 2, 10, i%60, 0, 0, time.Local)}
	}
	return out
}

func sizeTaskList(n, linked int) *service.TaskList {
	tl := &service.TaskList{ID: sizeID(60000), Name: "Findings", Tasks: make([]service.Task, n)}
	for i := range tl.Tasks {
		t := service.Task{ID: sizeID(60001 + i), Name: sizeText(70), Completed: i%3 == 0}
		if i < linked {
			link := sizeCard(70000+i, "Open")
			t.Linked = &link
		}
		tl.Tasks[i] = t
	}
	return tl
}

func sizeFullCard(descLen, comments, commentLen, attachments int, tl *service.TaskList) *service.Card {
	c := &service.Card{
		CardSummary: sizeCard(1, "Open"),
		Project:     "Projects", Type: "project",
		Description: sizeText(descLen),
		Comments:    sizeComments(comments, commentLen),
	}
	c.CommentsTotal = comments
	for i := range attachments {
		c.Attachments = append(c.Attachments, service.Attachment{Name: fmt.Sprintf("screenshot-%d.png", i), URL: "https://plan.example.com/attachments/" + sizeID(80000+i) + "/download/screenshot.png"})
	}
	if tl != nil {
		c.TaskLists = []service.TaskList{*tl}
	}
	return c
}

func sizeProjects(projects, boards int) []service.ProjectBoards {
	out := make([]service.ProjectBoards, projects)
	for i := range out {
		out[i].Project = fmt.Sprintf("Project %d", i)
		for j := range boards {
			out[i].Boards = append(out[i].Boards, service.BoardRef{ID: sizeID(90000 + i*100 + j), Name: fmt.Sprintf("Board %d of project %d", j, i)})
		}
	}
	return out
}

// Answer budgets in characters. A default answer is one the model did not ask
// to be bigger; an asked-for answer is one it widened with list, limit or
// before, or a whole card. Claude Code saves results over 50,000 characters
// to a file and warns from about 10,000 tokens (some 35,000 characters).
const (
	defaultAnswerChars = 10_000
	askedAnswerChars   = 25_000
)

// TestToolOutputSizes keeps every tool's answer on large data within its
// budget. Run it with -v to see the sizes.
func TestToolOutputSizes(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		args   map[string]any
		deps   func(t *testing.T) Deps
		budget int
		// exempt is text the budget leaves out: a card's description goes out
		// whole, because update_card replaces it.
		exempt int
	}{
		{"get_board 425 cards", "get_board", map[string]any{"board": "b"}, boardDeps("", sizeLists(60, 15, 310, 40)), defaultAnswerChars, 0},
		{"get_board 100 cards", "get_board", map[string]any{"board": "b"}, boardDeps("", sizeLists(30, 5, 60, 5)), defaultAnswerChars, 0},
		{"get_board 8 full active lists", "get_board", map[string]any{"board": "b"}, boardDeps("", wideLists(8, 30)), askedAnswerChars, 0},
		{"get_board list=Done 310", "get_board", map[string]any{"board": "b", "list": "Done"},
			boardDeps("Done", []service.List{{Name: "Done", Closed: true, Cards: make([]service.CardSummary, 310)}}), askedAnswerChars, 0},
		{"search_cards default", "search_cards", map[string]any{"query": "x"}, searchDeps(), defaultAnswerChars, 0},
		{"search_cards limit 500", "search_cards", map[string]any{"query": "x", "limit": 500}, searchDeps(), askedAnswerChars, 0},
		{"get_card typical", "get_card", map[string]any{"card": "c"}, cardDeps(sizeFullCard(1500, 3, 300, 2, sizeTaskList(7, 0))), defaultAnswerChars, 0},
		{"get_card 20k desc, 10x2k comments", "get_card", map[string]any{"card": "c"},
			cardDeps(sizeFullCard(20000, 10, 2000, 20, sizeTaskList(30, 10))), askedAnswerChars, 20000},
		{"get_card parent card, 50 links", "get_card", map[string]any{"card": "c"},
			cardDeps(sizeFullCard(2000, 3, 300, 0, sizeTaskList(50, 50))), askedAnswerChars, 2000},
		{"list_comments default, 1k each", "list_comments", map[string]any{"card": "c"}, commentDeps(sizeComments(50, 1000)), askedAnswerChars, 0},
		{"list_comments limit 500, 4k each", "list_comments", map[string]any{"card": "c", "limit": 500}, commentDeps(sizeComments(50, 4000)), askedAnswerChars, 0},
		{"list_boards 10x10", "list_boards", map[string]any{}, func(t *testing.T) Deps {
			t.Helper()
			catalog := NewMockCatalogService(t)
			catalog.EXPECT().ListBoards(mock.Anything, "").Return(sizeProjects(10, 10), nil)
			return Deps{Catalog: catalog}
		}, defaultAnswerChars, 0},
		{"add_tasks 100 tasks, 40 links", "add_tasks", map[string]any{"card": "c", "items": []map[string]any{{"name": "x"}}}, func(t *testing.T) Deps {
			t.Helper()
			tasks := NewMockTaskService(t)
			tasks.EXPECT().Add(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sizeTaskList(100, 40), nil)
			return Deps{Tasks: tasks}
		}, askedAnswerChars, 0},
		{"update_tasks 50 tasks", "update_tasks", map[string]any{"items": []map[string]any{{"task_id": "t", "completed": true}}}, func(t *testing.T) Deps {
			t.Helper()
			tasks := NewMockTaskService(t)
			tasks.EXPECT().Update(mock.Anything, mock.Anything).Return(sizeTaskList(50, 0).Tasks, nil)
			return Deps{Tasks: tasks}
		}, defaultAnswerChars, 0},
		{"add_comment 4k", "add_comment", map[string]any{"card": "c", "text": "x"}, func(t *testing.T) Deps {
			t.Helper()
			comments := NewMockCommentService(t)
			comments.EXPECT().Add(mock.Anything, mock.Anything, mock.Anything).Return(&sizeComments(1, 4000)[0], nil)
			return Deps{Comments: comments}
		}, defaultAnswerChars, 0},
		{"update_card", "update_card", map[string]any{"card": "c", "title": "x"}, func(t *testing.T) Deps {
			t.Helper()
			cards := NewMockCardService(t)
			cards.EXPECT().Update(mock.Anything, mock.Anything).Return(&service.WriteResult{Card: sizeCard(1, "Open")}, nil)
			return Deps{Cards: cards}
		}, defaultAnswerChars, 0},
		{"delete_card", "delete_card", map[string]any{"card": "c"}, func(t *testing.T) Deps {
			t.Helper()
			cards := NewMockCardService(t)
			card := sizeCard(1, "Open")
			cards.EXPECT().Delete(mock.Anything, mock.Anything).Return(&card, nil)
			return Deps{Cards: cards}
		}, defaultAnswerChars, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, connect(t, tc.deps(t)), tc.tool, tc.args)
			require.False(t, res.IsError)
			size := len(resultText(t, res))
			t.Logf("%-36s %7d chars", tc.name, size)
			assert.LessOrEqual(t, size-tc.exempt, tc.budget)
		})
	}
}

func boardDeps(list string, lists []service.List) func(t *testing.T) Deps {
	return func(t *testing.T) Deps {
		t.Helper()
		catalog := NewMockCatalogService(t)
		catalog.EXPECT().GetBoard(mock.Anything, "b", list).Return(sizeBoard(lists), nil)
		return Deps{Catalog: catalog}
	}
}

// wideLists is a board with many busy active lists and two closed ones.
func wideLists(active, cards int) []service.List {
	lists := make([]service.List, 0, active+2)
	for i := range active {
		lists = append(lists, service.List{Name: fmt.Sprintf("Step %d", i), Cards: make([]service.CardSummary, cards)})
	}
	return append(lists,
		service.List{Name: "Done", Closed: true, Cards: make([]service.CardSummary, 200)},
		service.List{Name: "Dropped", Closed: true, Cards: make([]service.CardSummary, 50)})
}

// searchDeps answers a search with as many cards as its limit allows, as the
// service does when plenty match.
func searchDeps() func(t *testing.T) Deps {
	return func(t *testing.T) Deps {
		t.Helper()
		cards := NewMockCardService(t)
		cards.EXPECT().Search(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, q service.SearchQuery) ([]service.CardSummary, int, error) {
			found := make([]service.CardSummary, q.Limit)
			for i := range found {
				found[i] = sizeCard(i+1, "Done")
			}
			return found, 40, nil
		})
		return Deps{Cards: cards}
	}
}

func cardDeps(card *service.Card) func(t *testing.T) Deps {
	return func(t *testing.T) Deps {
		t.Helper()
		cards := NewMockCardService(t)
		cards.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(card, nil)
		return Deps{Cards: cards}
	}
}

// commentDeps answers with Planka's page of comments cut to the limit, as the
// service does.
func commentDeps(page []service.Comment) func(t *testing.T) Deps {
	return func(t *testing.T) Deps {
		t.Helper()
		cards := NewMockCardService(t)
		cards.EXPECT().Comments(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, _ service.CardRef, _ string, limit int) ([]service.Comment, bool, error) {
				return page[:min(limit, len(page))], true, nil
			})
		return Deps{Cards: cards}
	}
}
