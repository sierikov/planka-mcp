package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

// cardLine is a card in a list or a search result: only what is needed to
// pick the next call. It never carries the description.
type cardLine struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Board     string   `json:"board,omitempty"`
	List      string   `json:"list,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Due       string   `json:"due,omitempty"`
	DueDone   bool     `json:"due_done,omitempty"`
	Tasks     string   `json:"tasks,omitempty"`
	// CommentsTotal is how many comments the card has; card lines leave out 0.
	CommentsTotal int `json:"comments_total,omitzero"`
}

type projectOut struct {
	Project string        `json:"project"`
	Boards  []boardRefOut `json:"boards"`
}

type boardRefOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type boardOut struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Project string    `json:"project,omitempty"`
	Labels  []string  `json:"labels,omitempty"`
	Members []string  `json:"members,omitempty"`
	Lists   []listOut `json:"lists,omitempty"`
	Hint    string    `json:"hint,omitempty"`
}

// listOut is a list with its first cards. More counts the cards left out.
type listOut struct {
	Name   string     `json:"name"`
	Closed bool       `json:"closed,omitempty"`
	Cards  []cardLine `json:"cards,omitempty"`
	More   int        `json:"more,omitzero"`
}

// cardOut is the full card, as get_card returns it.
type cardOut struct {
	cardLine
	Project      string                `json:"project,omitempty"`
	Type         string                `json:"type,omitempty"`
	Description  string                `json:"description,omitempty"`
	Stopwatch    string                `json:"stopwatch,omitempty"`
	TaskLists    []taskListOut         `json:"task_lists,omitempty"`
	Attachments  []attachmentOut       `json:"attachments,omitempty"`
	CustomFields []customFieldGroupOut `json:"custom_fields,omitempty"`
	// CommentsTotal is always present in the full card, so 0 says there are
	// none rather than that they were left out.
	CommentsTotal int          `json:"comments_total"`
	Comments      []commentOut `json:"comments,omitempty"`
	// Hint says how to read the comments left out.
	Hint string `json:"hint,omitempty"`
}

// customFieldGroupOut is a group of custom fields with values, in Planka's
// order. Group is left out when Planka's answer did not name the group.
type customFieldGroupOut struct {
	Group  string           `json:"group,omitempty"`
	Fields []customFieldOut `json:"fields"`
}

// customFieldOut is one field's value, verbatim: Planka stores plain text.
// ShowOnFrontOfCard is left out when false.
type customFieldOut struct {
	Name              string `json:"name"`
	Value             string `json:"value"`
	ShowOnFrontOfCard bool   `json:"show_on_front_of_card,omitzero"`
}

type attachmentOut struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type taskListOut struct {
	Name  string    `json:"name"`
	Tasks []taskOut `json:"tasks,omitempty"`
}

type taskOut struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Done bool      `json:"done,omitempty"`
	Link *cardLine `json:"link,omitempty"`
}

// searchOut is a page of search results. More counts the matches the limit
// left out, and Hint says how to see them.
type searchOut struct {
	Cards []cardLine `json:"cards"`
	More  int        `json:"more,omitzero"`
	Hint  string     `json:"hint,omitempty"`
}

// commentsOut is a page of comments. Older says that older comments exist or
// may, and Hint names the before value that reads them.
type commentsOut struct {
	Comments []commentOut `json:"comments"`
	Older    bool         `json:"older,omitzero"`
	Hint     string       `json:"hint,omitempty"`
}

type commentOut struct {
	ID     string `json:"id"`
	Author string `json:"author,omitempty"`
	Date   string `json:"date,omitempty"`
	Text   string `json:"text"`
}

// formatDate renders t in the server's local time zone: a plain date when it
// is local midnight, otherwise the date and the time.
func formatDate(t time.Time) string {
	t = t.In(time.Local)
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
		return t.Format(time.DateOnly)
	}
	return t.Format("2006-01-02 15:04")
}

func toCardLine(c service.CardSummary) cardLine {
	line := cardLine{
		ID: c.ID, Title: c.Title, Board: c.Board, List: c.List,
		Labels: c.Labels, Assignees: c.Assignees, DueDone: c.DueCompleted, CommentsTotal: c.CommentsTotal,
	}
	if c.Due != nil {
		line.Due = formatDate(*c.Due)
	}
	if c.TasksTotal > 0 {
		line.Tasks = fmt.Sprintf("%d/%d", c.TasksDone, c.TasksTotal)
	}
	return line
}

func toCardLines(cards []service.CardSummary) []cardLine {
	out := make([]cardLine, len(cards))
	for i, c := range cards {
		out[i] = toCardLine(c)
	}
	return out
}

func toProjects(projects []service.ProjectBoards) []projectOut {
	out := make([]projectOut, len(projects))
	for i, p := range projects {
		boards := make([]boardRefOut, len(p.Boards))
		for j, b := range p.Boards {
			boards[j] = boardRefOut{ID: b.ID, Name: b.Name}
		}
		out[i] = projectOut{Project: p.Project, Boards: boards}
	}
	return out
}

// toBoardOut shapes a board for get_board. Each list keeps its first cards, the
// ones at the top of the column, at most perList of them. Its card lines leave
// out board and list: the enclosing board and list already name them.
func toBoardOut(b *service.Board, perList func(service.List) int) boardOut {
	lists := make([]listOut, len(b.Lists))
	for i, l := range b.Lists {
		n := min(len(l.Cards), perList(l))
		cards := toCardLines(l.Cards[:n])
		for j := range cards {
			cards[j].Board, cards[j].List = "", ""
		}
		lists[i] = listOut{Name: l.Name, Closed: l.Closed, Cards: cards, More: len(l.Cards) - n}
	}
	return boardOut{ID: b.ID, Name: b.Name, Project: b.Project, Labels: b.Labels, Members: b.Members, Lists: lists}
}

func toCardOut(c *service.Card) cardOut {
	out := cardOut{
		cardLine:    toCardLine(c.CardSummary),
		Project:     c.Project,
		Type:        c.Type,
		Description: c.Description,
		Stopwatch:   stopwatchText(c.Stopwatch),
		Comments:    toComments(c.Comments),
	}
	out.CommentsTotal = c.CommentsTotal
	for _, tl := range c.TaskLists {
		out.TaskLists = append(out.TaskLists, toTaskListOut(tl))
	}
	for _, a := range c.Attachments {
		out.Attachments = append(out.Attachments, attachmentOut{Name: a.Name, URL: a.URL})
	}
	for _, g := range c.CustomFields {
		group := customFieldGroupOut{Group: g.Name, Fields: make([]customFieldOut, len(g.Fields))}
		for i, f := range g.Fields {
			group.Fields[i] = customFieldOut{Name: f.Name, Value: f.Value, ShowOnFrontOfCard: f.ShowOnFrontOfCard}
		}
		out.CustomFields = append(out.CustomFields, group)
	}
	return out
}

func toCommentOut(c service.Comment) commentOut {
	return commentOut{ID: c.ID, Author: c.Author, Date: formatDate(c.Created), Text: c.Text}
}

func toComments(comments []service.Comment) []commentOut {
	out := make([]commentOut, len(comments))
	for i, c := range comments {
		out[i] = toCommentOut(c)
	}
	return out
}

// fitComments keeps the first comments whose text fits in chars characters.
// The first one is always kept, however long, so a long comment is never
// skipped over.
func fitComments(comments []commentOut, chars int) []commentOut {
	used := 0
	for i, c := range comments {
		used += len(c.Text)
		if i > 0 && used > chars {
			return comments[:i]
		}
	}
	return comments
}

func toTaskOut(t service.Task) taskOut {
	out := taskOut{ID: t.ID, Name: t.Name, Done: t.Completed}
	if t.Linked != nil {
		link := toCardLine(*t.Linked)
		out.Link = &link
	}
	return out
}

func toTaskListOut(tl service.TaskList) taskListOut {
	out := taskListOut{Name: tl.Name}
	for _, t := range tl.Tasks {
		out.Tasks = append(out.Tasks, toTaskOut(t))
	}
	return out
}

// stopwatchText renders a card's stopwatch like "1h 30m (running)". It is
// empty when the card has none.
func stopwatchText(s *service.Stopwatch) string {
	if s == nil {
		return ""
	}
	minutes := int(s.Total.Minutes())
	text := strconv.Itoa(minutes) + "m"
	if minutes >= 60 {
		text = fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
	}
	if s.Running {
		text += " (running)"
	}
	return text
}

// jsonResult returns v as compact JSON in a single text content block.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // descriptions and comments go out verbatim, and <, > and & cost tokens
	if err := enc.Encode(v); err != nil {
		return errorResult(fmt.Errorf("encode result: %w", err))
	}
	raw := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
}

// errorResult turns err into a tool result with IsError set. Failures are
// results the model can act on, never protocol errors.
func errorResult(err error) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: errorText(err)}},
	}, nil, nil
}

// statusCoder is implemented by Planka's HTTP errors.
type statusCoder interface {
	error
	StatusCode() int
}

// errorText renders err for the model. A typed error found inside err gets its
// hint-rich text; the context err wraps it in (such as "card 5 was created,
// but: ") is kept, because it says what a failed write had already done.
func errorText(err error) string {
	var (
		ambiguous *service.AmbiguousError
		unknown   *service.UnknownNameError
		noMatch   *service.NoMatchError
		status    statusCoder
	)
	switch {
	case errors.As(err, &ambiguous):
		return inContext(err, ambiguous, ambiguousText(ambiguous))
	case errors.As(err, &unknown):
		return inContext(err, unknown, unknownNameText(unknown))
	case errors.As(err, &noMatch):
		return inContext(err, noMatch, noMatchText(noMatch))
	case errors.Is(err, service.ErrNotFound):
		return err.Error() + ": check the ID, or whether your Planka user can see it"
	case errors.As(err, &status):
		hint := statusHint(status.StatusCode())
		switch {
		case hint == "":
		case err.Error() == status.Error():
			return hint
		default:
			return err.Error() + "; " + hint
		}
	}
	return err.Error()
}

// statusHint is what to do about a Planka status, or "" for one that needs no hint.
func statusHint(code int) string {
	switch code {
	case http.StatusUnauthorized:
		return "Planka refused the credentials (401): the API key or login is wrong or has expired. In stdio mode they come from PLANKA_API_KEY or PLANKA_USERNAME and PLANKA_PASSWORD; in http mode the client sends the key as \"Authorization: Bearer <key>\""
	case http.StatusForbidden:
		return "Planka refused (403): you lack rights on this board, or Planka does not allow this change"
	}
	return ""
}

// inContext puts text, the rendering of inner, in the place of inner's own
// message in err's, so whatever err adds around inner survives.
func inContext(err, inner error, text string) string {
	full := err.Error()
	if full == inner.Error() {
		return text
	}
	if prefix, ok := strings.CutSuffix(full, inner.Error()); ok {
		return prefix + text
	}
	return full + "; " + text
}

// ambiguousText lists the candidates. When each has an ID, the retry goes by
// ID: the name alone is what was ambiguous.
func ambiguousText(e *service.AmbiguousError) string {
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
	return fmt.Sprintf("%q matches several %ss: %s; %s", e.Query, e.Kind, strings.Join(parts, "; "), retry)
}

func unknownNameText(e *service.UnknownNameError) string {
	text := fmt.Sprintf("no %s %q", e.Kind, e.Name)
	if e.Scope != "" {
		text += " on " + e.Scope
	}
	valid := "(none)"
	if len(e.Valid) > 0 {
		valid = strings.Join(e.Valid, ", ")
	}
	text = fmt.Sprintf("%s; valid %ss: %s", text, e.Kind, valid)
	if e.Hint != "" {
		text += "; " + e.Hint
	}
	return text
}

func noMatchText(e *service.NoMatchError) string {
	text := fmt.Sprintf("no %s matching %q", e.Kind, e.Query)
	if e.Scope != "" {
		text += " on " + e.Scope
	}
	if e.ArchiveHint {
		text += "; set include_archived to also search the archive"
	}
	return text
}
