package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

func TestCreateLabelOutput(t *testing.T) {
	labels := NewMockLabelService(t)
	labels.EXPECT().Create(mock.Anything, service.CreateLabel{Board: "B", Name: "release"}).
		Return(&service.LabelResult{ID: "l2", Name: "release", Color: "lagoon-blue"}, nil)

	res := call(t, connect(t, Deps{Labels: labels}), "create_label", map[string]any{"board": "B", "name": "release"})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"l2","name":"release","color":"lagoon-blue"}`, resultText(t, res))
}

func TestUpdateLabelOutput(t *testing.T) {
	labels := NewMockLabelService(t)
	labels.EXPECT().Update(mock.Anything, service.UpdateLabel{Board: "B", Label: "bug", Color: new("berry-red")}).
		Return(&service.LabelResult{ID: "l1", Name: "bug", Color: "berry-red", Cards: 3}, nil)

	res := call(t, connect(t, Deps{Labels: labels}), "update_label", map[string]any{"board": "B", "label": "bug", "color": "berry-red"})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"l1","name":"bug","color":"berry-red","cards_total":3}`, resultText(t, res))
}

func TestUpdateLabelRefusesAnUnknownColorWithTheList(t *testing.T) {
	// No mock call is expected: the schema refuses the colour.
	res := call(t, connect(t, Deps{Labels: NewMockLabelService(t)}), "update_label", map[string]any{"board": "B", "label": "bug", "color": "red"})

	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "color")
}

func TestUpdateLabelSchemaListsThePalette(t *testing.T) {
	res, err := connect(t, Deps{}).ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		if tool.Name != "update_label" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		assert.Equal(t, service.LabelColors, schema.Properties["color"].Enum)
		return
	}
	require.FailNow(t, "update_label is not registered")
}

func TestDeleteLabelListsTheCardsItLeft(t *testing.T) {
	labels := NewMockLabelService(t)
	labels.EXPECT().Delete(mock.Anything, "B", "bug", true).Return(&service.DeletedLabel{
		ID: "l1", Name: "bug", Color: "berry-red", More: 4,
		Cards: []service.CardSummary{{ID: "c1", Title: "Alpha", Board: "B", List: "Todo"}},
	}, nil)

	res := call(t, connect(t, Deps{Labels: labels}), "delete_label", map[string]any{"board": "B", "label": "bug", "from_cards": true})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"l1","name":"bug","color":"berry-red",`+
		`"cards":[{"id":"c1","title":"Alpha","board":"B","list":"Todo"}],"more":4}`, resultText(t, res))
}

func TestSetCustomFieldsOutput(t *testing.T) {
	fields := NewMockCustomFieldService(t)
	fields.EXPECT().Set(mock.Anything, service.CardRef{Ref: "c1"}, map[string]string{"Date of Stage 1": "2026-11-06", "Notes": ""}).
		Return([]service.CustomFieldGroup{
			{Name: "Interview", Fields: []service.CustomField{{Name: "Date of Stage 1", Value: "2026-11-06"}}, Unset: []string{"Notes"}},
		}, nil)

	res := call(t, connect(t, Deps{CustomFields: fields}), "set_custom_fields", map[string]any{
		"card": "c1", "fields": map[string]any{"Date of Stage 1": "2026-11-06", "Notes": ""},
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"custom_fields":[{"group":"Interview","fields":[{"name":"Date of Stage 1","value":"2026-11-06"}],"unset":["Notes"]}]}`,
		resultText(t, res))
}
