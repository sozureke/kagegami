package recon

import (
	"bytes"
	_ "embed" // the schema is part of the binary
	"encoding/json"
)

//go:embed delta.schema.json
var deltaSchema []byte

// Schema returns the JSON Schema of a StateDelta, as it is given to a model. It is written by
// hand; a test walks StateDelta with reflection and checks that every field and its type
// appear in it, and the other way round.
func Schema() json.RawMessage { return bytes.Clone(deltaSchema) }

// SchemaWithout returns the schema without the named top-level lists, for a caller that does not want
// a model to produce them.
func SchemaWithout(lists ...string) (json.RawMessage, error) {
	var doc map[string]any
	if err := json.Unmarshal(deltaSchema, &doc); err != nil {
		return nil, err
	}
	props, _ := doc["properties"].(map[string]any)
	for _, name := range lists {
		delete(props, name)
	}
	return json.Marshal(doc)
}

// ModelSchema returns the schema a model is asked to follow when it reads command output. It is
// the full schema with three things taken away: findings (they come from rules, from the person
// and from tier 2, so a banner cannot produce a line of the report), the host given by name (a
// model reports addresses), and the web path's host made required (a path with no host belongs
// nowhere).
func ModelSchema() (json.RawMessage, error) {
	var doc map[string]any
	if err := json.Unmarshal(deltaSchema, &doc); err != nil {
		return nil, err
	}
	props, _ := doc["properties"].(map[string]any)
	delete(props, "findings")
	paths, _ := props["web_paths"].(map[string]any)
	item, _ := paths["items"].(map[string]any)
	itemProps, _ := item["properties"].(map[string]any)
	delete(itemProps, "host")
	item["required"] = []any{"host_ip", "port", "proto", "path"}
	return json.Marshal(doc)
}
