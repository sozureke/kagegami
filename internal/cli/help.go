package cli

import (
	"bytes"
	_ "embed"
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:embed help.yaml
var helpYAML []byte

type helpText struct {
	Short string `yaml:"short"`
	Long  string `yaml:"long"`
}

type helpDoc struct {
	Root     helpText            `yaml:"root"`
	Commands map[string]helpText `yaml:"commands"`
}

func loadHelp() (helpDoc, error) {
	var doc helpDoc
	dec := yaml.NewDecoder(bytes.NewReader(helpYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return helpDoc{}, fmt.Errorf("parse help.yaml: %w", err)
	}
	return doc, nil
}

var help = mustLoadHelp()

func mustLoadHelp() helpDoc {
	doc, err := loadHelp()
	if err != nil {
		panic(err)
	}
	return doc
}
