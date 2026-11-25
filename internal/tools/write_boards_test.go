package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/sierikov/planka-mcp/internal/service"
)

func TestCreateBoardReturnsTheBoardAsGetBoard(t *testing.T) {
	boards := NewMockBoardService(t)
	boards.EXPECT().Create(mock.Anything, service.CreateBoard{Project: "Work", Name: "Roadmap", Lists: []string{"Todo"}, ClosedList: "Done"}).
		Return(&service.Board{ID: "b9", Name: "Roadmap", Project: "Work", Lists: []service.List{
			{ID: "l1", Name: "Todo"}, {ID: "l2", Name: "Done", Closed: true},
		}}, nil)

	res := call(t, connect(t, Deps{Boards: boards}), "create_board", map[string]any{
		"project": "Work", "name": "Roadmap", "lists": []string{"Todo"}, "closed_list": "Done",
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"b9","name":"Roadmap","project":"Work","lists":[{"name":"Todo"},{"name":"Done","closed":true}]}`,
		resultText(t, res))
}

func TestDeleteBoardOutput(t *testing.T) {
	boards := NewMockBoardService(t)
	boards.EXPECT().Delete(mock.Anything, "Old", true).Return(&service.DeletedBoard{ID: "b1", Name: "Old", Project: "Work", Cards: 53, AtLeast: true}, nil)

	res := call(t, connect(t, Deps{Boards: boards}), "delete_board", map[string]any{"board": "Old", "with_cards": true})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"b1","name":"Old","project":"Work","cards_total":53,"cards_at_least":true}`, resultText(t, res))
}

func TestMemberToolsOutput(t *testing.T) {
	boards := NewMockBoardService(t)
	bob := &service.Member{ID: "m1", UserID: "u2", Name: "Bob", Username: "bob", Role: "viewer", CanComment: true}
	boards.EXPECT().AddMember(mock.Anything, service.AddMember{Board: "B", Person: "bob", Role: "viewer", CanComment: new(true)}).Return(bob, nil)
	boards.EXPECT().UpdateMember(mock.Anything, service.UpdateMember{Board: "B", Person: "bob", Role: new("viewer")}).Return(bob, nil)
	boards.EXPECT().RemoveMember(mock.Anything, "B", "bob").Return(bob, nil)
	s := connect(t, Deps{Boards: boards})

	res := call(t, s, "add_board_member", map[string]any{"board": "B", "person": "bob", "role": "viewer", "can_comment": true})
	assert.JSONEq(t, `{"name":"Bob","username":"bob","role":"viewer","can_comment":true}`, resultText(t, res))

	res = call(t, s, "update_board_member", map[string]any{"board": "B", "person": "bob", "role": "viewer"})
	assert.False(t, res.IsError, resultText(t, res))

	res = call(t, s, "remove_board_member", map[string]any{"board": "B", "person": "bob"})
	assert.JSONEq(t, `{"removed":"Bob","role":"viewer"}`, resultText(t, res))

	res = call(t, s, "add_board_member", map[string]any{"board": "B", "person": "bob", "role": "owner"})
	assert.True(t, res.IsError, "the schema allows editor and viewer only")
}
