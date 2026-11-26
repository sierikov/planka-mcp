package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

// maxDiffChars is the most of a description diff a result carries.
const maxDiffChars = 4000

type editDescriptionInput struct {
	Card    string                 `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board   string                 `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Version string                 `json:"version,omitempty" jsonschema:"the description_version get_card gave; the edit is refused if the description changed since"`
	Edits   []descriptionEditInput `json:"edits" jsonschema:"the changes, each found in the description as it is now; if one fails nothing is written"`
}

type descriptionEditInput struct {
	Section string  `json:"section,omitempty" jsonschema:"outline id such as 2.1 (get_card description=outline); with find, the search is limited to it"`
	Body    *string `json:"body,omitempty" jsonschema:"replace the section's text under its heading, subsections included"`
	Append  *string `json:"append,omitempty" jsonschema:"add lines at the end of the section, or of the whole description without section"`
	Find    *string `json:"find,omitempty" jsonschema:"exact text to replace; it must occur exactly once"`
	Replace *string `json:"replace,omitempty" jsonschema:"the text that replaces find; may be empty or span lines"`
}

// editedDescriptionOut is a description after an edit: a unified diff, the
// new version for the next edit, and the sections changed.
type editedDescriptionOut struct {
	Card     cardLine `json:"card"`
	Diff     string   `json:"diff,omitempty"`
	Version  string   `json:"description_version,omitempty"`
	Sections []string `json:"sections,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

// registerDescriptionTools adds the tool that edits part of a description.
func registerDescriptionTools(server *mcp.Server, cards CardService) {
	addTool(server, &mcp.Tool{
		Name:  "edit_description",
		Title: "Edit description",
		Description: "Use this to change part of a Planka card's description without resending it: replace or extend a section, or replace exact text. " +
			"Read get_card description=outline first and pass its description_version. The rest of the text is kept as it is. " +
			"Returns a unified diff and the new description_version.",
		Annotations: idempotent,
	}, editDescription(cards))
}

func editDescription(cards CardService) func(context.Context, *mcp.CallToolRequest, editDescriptionInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in editDescriptionInput) (*mcp.CallToolResult, any, error) {
		edits := make([]service.DescriptionEdit, len(in.Edits))
		for i, e := range in.Edits {
			edits[i] = service.DescriptionEdit{Section: e.Section, Body: e.Body, Append: e.Append, Find: e.Find, Replace: e.Replace}
		}
		res, err := cards.EditDescription(ctx, service.EditDescription{
			Card: service.CardRef{Ref: in.Card, Board: in.Board}, Version: in.Version, Edits: edits,
		})
		if err != nil {
			return errorResult(err)
		}
		out := editedDescriptionOut{Card: toCardLine(res.Card), Version: res.Version, Sections: res.Sections}
		out.Diff, out.Hint = cutDiff(res.Diff)
		if res.Diff == "" {
			out.Hint = "the edits changed nothing, so nothing was written"
		}
		return jsonResult(out)
	}
}

// cutDiff keeps a diff within maxDiffChars, cut at a line, and says how many
// lines it left out.
func cutDiff(diff string) (kept, hint string) {
	if len(diff) <= maxDiffChars {
		return diff, ""
	}
	cut := strings.LastIndexByte(diff[:maxDiffChars], '\n') + 1
	left := strings.Count(diff[cut:], "\n")
	return diff[:cut], fmt.Sprintf("the diff was cut: %d more lines; read the changed sections with get_card to check them", left)
}
