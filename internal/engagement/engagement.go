// Package engagement sets and reads what a case is about: its target, goal, scope, rules and
// phase. The case database is the only place they are kept; every way of setting them (the
// case command, the flags of watch) goes through Update.
package engagement

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sozureke/kagegami/internal/scope"
	"github.com/sozureke/kagegami/internal/store"
)

// ErrInvalid is wrapped by errors about a value that cannot be used.
var ErrInvalid = errors.New("invalid engagement setting")

// Changes lists what to change. A nil field is left as it is.
type Changes struct {
	Target *string
	Goal   *string
	ROE    *string
	Phase  *string
	// Scope replaces the scope with these entries; an empty (non-nil) list empties it.
	Scope *[]string
	// AddScope adds entries to the scope.
	AddScope []string
}

// Result is the engagement after the update.
type Result struct {
	Engagement store.Engagement
	// ScopeFromTarget says that the scope was empty and was set to the target.
	ScopeFromTarget bool
}

const (
	maxTarget = 255
	maxGoal   = 500
	maxROE    = 4000
)

var phaseRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Update applies the changes to the engagement of the case and saves it. The case gets the name
// it is called by. If the scope is empty after the change and the target is an address, a network or
// a range, the scope becomes the target: the usual case of one box needs one setting.
func Update(ctx context.Context, st *store.Store, caseName string, c Changes) (Result, error) {
	var res Result
	err := st.InTx(ctx, func(r *store.Repo) error {
		e, err := r.GetEngagement(ctx)
		if errors.Is(err, store.ErrNotFound) {
			e, err = store.Engagement{Name: caseName, Phase: "recon"}, nil
		}
		if err != nil {
			return err
		}
		if e.Name == "" {
			e.Name = caseName
		}

		if c.Target != nil {
			if e.Target, err = text("target", *c.Target, maxTarget, false); err != nil {
				return err
			}
		}
		if c.Goal != nil {
			if e.Goal, err = text("goal", *c.Goal, maxGoal, true); err != nil {
				return err
			}
		}
		if c.ROE != nil {
			if e.ROE, err = text("rules of engagement", *c.ROE, maxROE, true); err != nil {
				return err
			}
		}
		if c.Phase != nil {
			p := strings.TrimSpace(*c.Phase)
			if !phaseRE.MatchString(p) {
				return fmt.Errorf("%w: phase %q (lower case letters, digits and dashes, starting with a letter)", ErrInvalid, p)
			}
			e.Phase = p
		}

		sc, err := scope.ParseText(e.Scope)
		if err != nil {
			sc = scope.Scope{} // a scope that was stored in a bad form is replaced, never trusted
		}
		if c.Scope != nil {
			if sc, err = scope.Parse(*c.Scope); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalid, err)
			}
		}
		if len(c.AddScope) > 0 {
			if sc, err = sc.Add(c.AddScope...); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalid, err)
			}
		}
		if sc.IsEmpty() && e.Target != "" {
			if fromTarget, terr := scope.ParseText(e.Target); terr == nil && !fromTarget.IsEmpty() {
				sc, res.ScopeFromTarget = fromTarget, true
			}
		}
		e.Scope = sc.String()

		saved, err := r.SaveEngagement(ctx, e)
		res.Engagement = saved
		return err
	})
	return res, err
}

// Get returns the engagement of the case, or ErrNotFound if nothing was set.
func Get(ctx context.Context, st *store.Store) (store.Engagement, error) {
	return st.Repo().GetEngagement(ctx)
}

// Scope returns the scope of an engagement. A scope that cannot be read is empty: nothing is
// in scope until it is set again.
func Scope(e store.Engagement) scope.Scope {
	s, err := scope.ParseText(e.Scope)
	if err != nil {
		return scope.Scope{}
	}
	return s
}

func text(what, v string, limit int, mayBeEmpty bool) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" && !mayBeEmpty {
		return "", fmt.Errorf("%w: the %s must not be empty", ErrInvalid, what)
	}
	if len([]rune(v)) > limit {
		return "", fmt.Errorf("%w: the %s is longer than %d characters", ErrInvalid, what, limit)
	}
	for _, r := range v {
		if unicode.IsControl(r) && (r != '\n' || !mayBeEmpty) {
			return "", fmt.Errorf("%w: the %s has a control character", ErrInvalid, what)
		}
	}
	return v, nil
}
