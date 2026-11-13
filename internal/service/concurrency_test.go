package service

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// gate holds each caller until a second one arrives, or a short time has
// passed. A fake holds a create in it before changing anything, so that two
// unserialized writes both read the board before either one's create lands.
type gate struct {
	mu      sync.Mutex
	arrived int
	open    chan struct{}
}

func newGate() *gate { return &gate{open: make(chan struct{})} }

func (g *gate) wait() {
	g.mu.Lock()
	g.arrived++
	if g.arrived == 2 {
		close(g.open)
	}
	g.mu.Unlock()
	select {
	case <-g.open:
	case <-time.After(200 * time.Millisecond):
	}
}

// fakeBoard is board B1 as Planka keeps it: reads see what the writes so far
// have made.
type fakeBoard struct {
	mu   sync.Mutex
	resp *planka.BoardResponse
	gate *gate // holds CreateLabel
}

func (b *fakeBoard) read(context.Context, string) (*planka.BoardResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := *b.resp
	r.Included.Cards = slices.Clone(r.Included.Cards)
	r.Included.Labels = slices.Clone(r.Included.Labels)
	r.Included.CardLabels = slices.Clone(r.Included.CardLabels)
	return &r, nil
}

func (b *fakeBoard) createCard(_ context.Context, listID string, req planka.CreateCardRequest) (*planka.Card, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := planka.Card{ID: fmt.Sprintf("n%d", len(b.resp.Included.Cards)), BoardID: "b1", ListID: listID, Name: req.Name, Position: &req.Position}
	b.resp.Included.Cards = append(b.resp.Included.Cards, c)
	return &c, nil
}

func (b *fakeBoard) createLabel(_ context.Context, boardID, name, color string, pos float64) (*planka.Label, error) {
	b.gate.wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	l := planka.Label{ID: fmt.Sprintf("l%d", len(b.resp.Included.Labels)), BoardID: boardID, Name: &name, Color: color, Position: pos}
	b.resp.Included.Labels = append(b.resp.Included.Labels, l)
	return &l, nil
}

func (b *fakeBoard) addCardLabel(_ context.Context, cardID, labelID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resp.Included.CardLabels = append(b.resp.Included.CardLabels, planka.CardLabel{CardID: cardID, LabelID: labelID})
	return nil
}

func TestCreateConcurrentCallsCreateALabelOnce(t *testing.T) {
	f := newCardsFixture(t)
	board := &fakeBoard{resp: writeBoard(), gate: newGate()}
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").RunAndReturn(board.read)
	f.write.EXPECT().CreateCard(mock.Anything, "b1-todo", mock.Anything).RunAndReturn(board.createCard)
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "release", mock.Anything, mock.Anything).RunAndReturn(board.createLabel)
	f.labels.EXPECT().AddCardLabel(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(board.addCardLabel)

	var wg sync.WaitGroup
	for _, title := range []string{"Tag 0.3", "Publish 0.3"} {
		wg.Go(func() {
			_, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: title, Labels: []string{"release"}, CreateLabels: true})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	var release []string
	for _, l := range board.resp.Included.Labels {
		if labelName(l) == "release" {
			release = append(release, l.ID)
		}
	}
	require.Len(t, release, 1, "the second call must see the label the first one created")
	tagged := 0
	for _, cl := range board.resp.Included.CardLabels {
		if cl.LabelID == release[0] {
			tagged++
		}
	}
	assert.Equal(t, 2, tagged, "both new cards carry the label")
}

// fakeCard is card c1 on board B1 as Planka keeps it, with its checklists.
type fakeCard struct {
	mu    sync.Mutex
	lists []planka.TaskList
	tasks []planka.Task
	gate  *gate // holds CreateTaskList
}

func (c *fakeCard) read(context.Context, string) (*planka.CardResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cardOf(slices.Clone(c.lists), slices.Clone(c.tasks)...), nil
}

func (c *fakeCard) createTaskList(_ context.Context, cardID, name string, pos float64) (*planka.TaskList, error) {
	c.gate.wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	tl := planka.TaskList{ID: fmt.Sprintf("tl%d", len(c.lists)), CardID: cardID, Name: name, Position: pos}
	c.lists = append(c.lists, tl)
	return &tl, nil
}

func (c *fakeCard) createTask(_ context.Context, listID string, req planka.CreateTaskRequest) (*planka.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tk := planka.Task{ID: fmt.Sprintf("t%d", len(c.tasks)), TaskListID: listID, Name: req.Name, Position: req.Position}
	c.tasks = append(c.tasks, tk)
	return &tk, nil
}

func TestAddTasksConcurrentCallsCreateOneChecklist(t *testing.T) {
	f := newTasksFixture(t)
	card := &fakeCard{gate: newGate()}
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil)
	f.cards.EXPECT().Card(mock.Anything, "c1").RunAndReturn(card.read)
	f.api.EXPECT().CreateTaskList(mock.Anything, "c1", "Tasks", mock.Anything).RunAndReturn(card.createTaskList)
	f.api.EXPECT().CreateTask(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(card.createTask)

	var wg sync.WaitGroup
	for _, name := range []string{"Write", "Test"} {
		wg.Go(func() {
			_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: name}})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	require.Len(t, card.lists, 1, "the second call must see the checklist the first one created")
	assert.Len(t, card.tasks, 2)
	for _, tk := range card.tasks {
		assert.Equal(t, card.lists[0].ID, tk.TaskListID)
	}
}

func TestBoardLocksWaitEndsWithTheContext(t *testing.T) {
	var locks boardLocks
	unlock, err := locks.lock(t.Context(), "b1")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = locks.lock(ctx, "b1")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	other, err := locks.lock(t.Context(), "b2")
	require.NoError(t, err, "another board is not held up")
	other()

	unlock()
	again, err := locks.lock(t.Context(), "b1")
	require.NoError(t, err)
	again()
}
