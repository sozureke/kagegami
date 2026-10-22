package recon

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// jsonShape describes a Go type the way the schema does, as a map from the JSON name of each
// field to its schema type, recursively.
func goShape(t reflect.Type) (any, error) {
	switch t.Kind() {
	case reflect.Pointer:
		return goShape(t.Elem())
	case reflect.String:
		return "string", nil
	case reflect.Int:
		return "integer", nil
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
	return nil, fmt.Errorf("type %s is not handled by the schema test: extend it together with the schema", t)
}

func schemaShape(node map[string]any) (any, error) {
	switch node["type"] {
	case "string", "integer":
		return node["type"], nil
	case "array":
		item, err := schemaShape(node["items"].(map[string]any))
		if err != nil {
			return nil, err
		}
		return []any{item}, nil
	case "object":
		out := map[string]any{}
		for name, sub := range node["properties"].(map[string]any) {
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

// The hand-written schema and the Go types must say the same: a field added on one side and not
// the other, or of another type, fails here.
func TestSchemaMatchesStateDelta(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	want, err := goShape(reflect.TypeOf(StateDelta{}))
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
		t.Errorf("the schema and StateDelta differ.\nschema:\n%s\nGo types:\n%s", g, w)
	}
}

// Every object in the schema forbids other fields, and required fields exist.
func TestSchemaIsStrict(t *testing.T) {
	var doc map[string]any
	_ = json.Unmarshal(Schema(), &doc)
	var walk func(path string, node map[string]any)
	walk = func(path string, node map[string]any) {
		switch node["type"] {
		case "object":
			if node["additionalProperties"] != false {
				t.Errorf("%s allows unknown fields", path)
			}
			props := node["properties"].(map[string]any)
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

// A schema's enums are the values Validate accepts.
func TestSchemaEnumsMatchValidate(t *testing.T) {
	var doc struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(Schema(), &doc); err != nil {
		t.Fatal(err)
	}
	check := func(list, field string, accepted []string) {
		got := doc.Properties[list].Items.Properties[field].Enum
		if !slices.Equal(got, accepted) {
			t.Errorf("%s.%s: schema %v, Validate %v", list, field, got, accepted)
		}
	}
	check("hosts", "status", statuses)
	check("services", "proto", protocols)
	check("services", "state", states)
	check("web_paths", "proto", protocols)
	check("findings", "severity", severities)
}

func TestSchemaWithout(t *testing.T) {
	s, err := SchemaWithout("findings")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(s, &doc); err != nil {
		t.Fatal(err)
	}
	props := doc["properties"].(map[string]any)
	if _, ok := props["findings"]; ok || len(props) != 4 {
		t.Errorf("properties = %v", props)
	}
	// the original is not changed
	var full map[string]any
	_ = json.Unmarshal(Schema(), &full)
	if _, ok := full["properties"].(map[string]any)["findings"]; !ok {
		t.Error("SchemaWithout changed the shared schema")
	}
	if a, b := Schema(), Schema(); &a[0] == &b[0] {
		t.Error("Schema returns shared memory")
	}
}

func TestModelSchema(t *testing.T) {
	s, err := ModelSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(s, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Properties["findings"]; ok || len(doc.Properties) != 4 {
		t.Errorf("properties = %v", doc.Properties)
	}
	wp := doc.Properties["web_paths"].Items
	if _, ok := wp.Properties["host"]; ok || !slices.Contains(wp.Required, "host_ip") || len(wp.Required) != 4 {
		t.Errorf("web_paths = %+v", wp)
	}
	if _, ok := wp.Properties["host_ip"]; !ok {
		t.Error("host_ip must stay")
	}
	// the shared schema is untouched
	var full map[string]any
	_ = json.Unmarshal(Schema(), &full)
	if _, ok := full["properties"].(map[string]any)["findings"]; !ok {
		t.Error("ModelSchema changed the shared schema")
	}
}
