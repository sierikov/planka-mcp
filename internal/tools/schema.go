package tools

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// inputSchema is the input schema mcp.AddTool would infer for In, with each
// named property limited to the given values. The SDK checks every call
// against it, so a value outside the list is refused before the tool runs, and
// the model sees the choices up front instead of learning them from an error.
func inputSchema[In any](values map[string][]string) *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("input schema of %T: %v", *new(In), err))
	}
	for name, vs := range values {
		p, ok := s.Properties[name]
		if !ok {
			panic(fmt.Sprintf("input schema of %T has no property %q", *new(In), name))
		}
		for _, v := range vs {
			p.Enum = append(p.Enum, v)
		}
	}
	return s
}
