package tools

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/sierikov/planka-mcp/internal/service"
)

func TestCreateListOutput(t *testing.T) {
	lists := NewMockListService(t)
	lists.EXPECT().Create(mock.Anything, service.CreateList{
		Board: "B", Name: "Review", Place: service.ListPlace{After: "Doing"}, Closed: true, Color: "lagoon-blue",
	}).Return(&service.ListResult{
		List:  service.Column{ID: "l9", Name: "Review", Color: "lagoon-blue", Closed: true, Cards: 1},
		Order: []string{"Todo", "Doing", "Review"},
	}, nil)

	res := call(t, connect(t, Deps{Lists: lists}), "create_list", map[string]any{
		"board": "B", "name": "Review", "after": "Doing", "closed": true, "color": "lagoon-blue",
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"list":{"id":"l9","name":"Review","color":"lagoon-blue","closed":true,"cards_total":1},"list_order":["Todo","Doing","Review"]}`,
		resultText(t, res))
}

func TestUpdateListOutput(t *testing.T) {
	lists := NewMockListService(t)
	lists.EXPECT().Update(mock.Anything, service.UpdateList{
		Board: "B", List: "Done", Name: new("Finished"), Color: "none", Closed: new(true), Place: service.ListPlace{Before: "Todo"},
		SortBy: "due_date", SortOrder: "desc", ArchiveCards: true,
	}).Return(&service.ListResult{
		List: service.Column{ID: "l2", Name: "Finished", Closed: true}, Order: []string{"Finished", "Todo"}, Archived: 4,
	}, nil)

	res := call(t, connect(t, Deps{Lists: lists}), "update_list", map[string]any{
		"board": "B", "list": "Done", "name": "Finished", "color": "none", "closed": true, "before": "Todo",
		"sort_cards": "due_date", "sort_order": "desc", "archive_cards": true,
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"list":{"id":"l2","name":"Finished","closed":true,"cards_total":0},"list_order":["Finished","Todo"],"archived":4}`,
		resultText(t, res))
}

func TestDeleteListOutput(t *testing.T) {
	lists := NewMockListService(t)
	lists.EXPECT().Delete(mock.Anything, "B", "Old").Return(&service.DeletedList{ID: "l3", Name: "Old", CardsToTrash: 2}, nil)

	res := call(t, connect(t, Deps{Lists: lists}), "delete_list", map[string]any{"board": "B", "list": "Old"})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"l3","name":"Old","cards_moved_to_trash":2}`, resultText(t, res))
}

func TestListToolErrorsAreToolResults(t *testing.T) {
	lists := NewMockListService(t)
	lists.EXPECT().Delete(mock.Anything, "B", "Nope").Return(nil, errors.New(`no list "Nope"`))

	res := call(t, connect(t, Deps{Lists: lists}), "delete_list", map[string]any{"board": "B", "list": "Nope"})

	assert.True(t, res.IsError)
	assert.Equal(t, `no list "Nope"`, resultText(t, res))
}

// TestListSchemasOfferTheChoices: colour, sort and order are enums built from
// the service's values, so the model sees the choices, and a value outside
// them is refused before the service is called (the mock expects nothing).
func TestListSchemasOfferTheChoices(t *testing.T) {
	session := connect(t, Deps{Lists: NewMockListService(t)})
	props := inputProps(t, session)

	assert.Equal(t, toAny(service.ListColors), props["create_list"]["color"]["enum"])
	assert.Equal(t, append(toAny(service.ListColors), service.NoColor), props["update_list"]["color"]["enum"])
	assert.Equal(t, toAny(service.ListSorts), props["update_list"]["sort_cards"]["enum"])
	assert.Equal(t, toAny(service.SortOrders), props["update_list"]["sort_order"]["enum"])

	bad := call(t, session, "create_list", map[string]any{"board": "B", "name": "X", "color": "green"})
	assert.True(t, bad.IsError, "a value outside the enum must be refused")
	text := resultText(t, bad)
	assert.Contains(t, text, "green")
	assert.Contains(t, text, "berry-red", "the refusal lists the valid colours")
}
