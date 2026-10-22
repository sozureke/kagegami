// Package schematest checks that a hand-written JSON Schema and the Go type it describes say the
// same thing. It is for tests only.
package schematest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Check fails the test if the schema and the type differ in any field name or field type, in
// either direction: a field added to the type and not to the schema, a field left in the schema
// after it was removed from the type, or a field of another type.
func Check(t *testing.T, v any, schema []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	want, err := goShape(reflect.TypeOf(v))
	if err != nil {
		t.Fatal(err)
	}
	got, err := schemaShape(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("the schema and %T differ.\nschema:\n%s\nGo type:\n%s", v, g, w)
	}
}

// Strict fails the test if an object in the schema allows fields that it does not list, or
// requires a field that it does not list.
func Strict(t *testing.T, schema []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	var walk func(path string, node map[string]any)
	walk = func(path string, node map[string]any) {
		switch node["type"] {
		case "object":
			if node["additionalProperties"] != false {
				t.Errorf("%s allows unknown fields", path)
			}
			props, _ := node["properties"].(map[string]any)
			required, _ := node["required"].([]any)
			for _, r := range required {
				if _, ok := props[r.(string)]; !ok {
					t.Errorf("%s requires %v, which is not a property", path, r)
				}
			}
			for name, sub := range props {
				walk(path+"."+name, sub.(map[string]any))
			}
		case "array":
			walk(path+"[]", node["items"].(map[string]any))
		}
	}
	walk("$", doc)
}

func goShape(t reflect.Type) (any, error) {
	switch t.Kind() {
	case reflect.Pointer:
		return goShape(t.Elem())
	case reflect.String:
		return "string", nil
	case reflect.Int:
		return "integer", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Slice:
		item, err := goShape(t.Elem())
		if err != nil {
			return nil, err
		}
		return []any{item}, nil
	case reflect.Struct:
		fields := map[string]any{}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				return nil, fmt.Errorf("field %s.%s has no json name", t.Name(), f.Name)
			}
			shape, err := goShape(f.Type)
			if err != nil {
				return nil, err
			}
			fields[name] = shape
		}
		return fields, nil
	}
	return nil, fmt.Errorf("type %s is not handled by schematest: extend it together with the schema", t)
}

func schemaShape(node map[string]any) (any, error) {
	switch node["type"] {
	case "string", "integer", "boolean":
		return node["type"], nil
	case "array":
		item, err := schemaShape(node["items"].(map[string]any))
		if err != nil {
			return nil, err
		}
		return []any{item}, nil
	case "object":
		out := map[string]any{}
		props, _ := node["properties"].(map[string]any)
		for name, sub := range props {
			shape, err := schemaShape(sub.(map[string]any))
			if err != nil {
				return nil, err
			}
			out[name] = shape
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown schema type %v", node["type"])
}
