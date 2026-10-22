package recon

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/sozureke/kagegami/internal/schematest"
)

// The hand-written schema and the Go types must say the same: a field added on one side and not
// the other, or of another type, fails here.
func TestSchemaMatchesStateDelta(t *testing.T) { schematest.Check(t, StateDelta{}, Schema()) }

// Every object in the schema forbids other fields, and required fields exist.
func TestSchemaIsStrict(t *testing.T) { schematest.Strict(t, Schema()) }

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
