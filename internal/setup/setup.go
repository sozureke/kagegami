// Package setup creates the on-disk layout used by Kagegami: the config file,
// the report template and, optionally, a case directory.
package setup

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sozureke/kagegami/internal/config"
	"github.com/sozureke/kagegami/internal/store"
)

//go:embed assets/config.yaml
var defaultConfig []byte

//go:embed assets/pentest.md
var defaultTemplate []byte

const (
	dirMode  = 0o700
	fileMode = 0o600
)

type Action string

const (
	Created     Action = "created"
	Skipped     Action = "skipped"
	Overwritten Action = "overwritten"
)

// Item describes what happened to one path.
type Item struct {
	Path   string
	Action Action
	Backup string // set when an existing file was moved aside before being overwritten
}

type Result struct{ Items []Item }

type Options struct {
	Force bool   // overwrite existing file after back-up
	Case  string // when set, also create this case dir
}

func Init(p config.Paths, opts Options) (Result, error) {
	var res Result

	if opts.Case != "" {
		if err := config.ValidateCaseName(opts.Case); err != nil {
			return res, err
		}
	}

	for _, dir := range []string{p.ConfigDir(), p.TemplatesDir()} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return res, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	files := []struct {
		path string
		data []byte
	}{
		{p.ConfigFile(), defaultConfig},
		{filepath.Join(p.TemplatesDir(), "pentest.md"), defaultTemplate},
	}
	for _, f := range files {
		item, err := writeFile(f.path, f.data, opts.Force)
		if err != nil {
			return res, err
		}
		res.Items = append(res.Items, item)
	}

	if opts.Case != "" {
		item, err := createCase(p, opts.Case)
		if err != nil {
			return res, err
		}
		res.Items = append(res.Items, item)
	}
	return res, nil
}

func writeFile(path string, data []byte, force bool) (Item, error) {
	err := create(path, data)
	if err == nil {
		return Item{Path: path, Action: Created}, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return Item{}, fmt.Errorf("create %s: %w", path, err)
	}
	if !force {
		return Item{Path: path, Action: Skipped}, nil
	}

	backup := path + ".bak"
	if err := os.Rename(path, backup); err != nil { // replaces an older backup
		return Item{}, fmt.Errorf("back up %s: %w", path, err)
	}
	if err := create(path, data); err != nil {
		return Item{}, fmt.Errorf("create %s: %w", path, err)
	}

	return Item{Path: path, Action: Overwritten, Backup: backup}, nil
}

func create(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // path is built from the resolved config locations
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = f.Write(data)
	return err
}

func createCase(p config.Paths, name string) (Item, error) {
	dir, err := p.CaseDir(name)
	if err != nil {
		return Item{}, err
	}
	raw := filepath.Join(dir, "raw")

	_, statErr := os.Stat(raw)
	existed := statErr == nil
	if err := os.MkdirAll(raw, dirMode); err != nil {
		return Item{}, fmt.Errorf("create case %q: %w", name, err)
	}

	// Opening creates the database and brings its schema up to date; for an existing
	// case that is a no-op when the schema is already current.
	st, err := store.Open(context.Background(), filepath.Join(dir, store.FileName))
	if err != nil {
		return Item{}, fmt.Errorf("create case %q: %w", name, err)
	}
	if err := st.Close(); err != nil {
		return Item{}, fmt.Errorf("create case %q: %w", name, err)
	}

	if existed {
		return Item{Path: dir, Action: Skipped}, nil
	}
	return Item{Path: dir, Action: Created}, nil
}
