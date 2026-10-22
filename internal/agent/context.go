package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/sozureke/kagegami/internal/advice"
	"github.com/sozureke/kagegami/internal/engagement"
	"github.com/sozureke/kagegami/internal/mapview"
	"github.com/sozureke/kagegami/internal/store"
)

// ContextOptions bound what tier 2 is given.
type ContextOptions struct {
	// MapBytes bounds the text of the map; zero means 6 KiB.
	MapBytes int
	// JournalEntries is how many of the latest journal entries are given whole; zero means 12. Older
	// entries are folded into a summary.
	JournalEntries int
	// PathsPerService bounds the web paths listed under a service; zero means 12.
	PathsPerService int
}

func (o ContextOptions) withDefaults() ContextOptions {
	if o.MapBytes <= 0 {
		o.MapBytes = 6 << 10
	}
	if o.JournalEntries <= 0 {
		o.JournalEntries = 12
	}
	if o.PathsPerService <= 0 {
		o.PathsPerService = 12
	}
	return o
}

// AdviceContext is everything tier 2 reads and the checks on its answer.
type AdviceContext struct {
	// Case is what the engagement is about, as the tester wrote it.
	Case string
	// Map is the map as dense text. It holds text that came from the target.
	Map string
	// Journal is the folded journal. Entries that did not come from the tester hold text from the
	// target or from tools.
	Journal string
	// Check is what the answer of the model is checked against.
	Check advice.Context
	// Empty says that nothing is known yet: no host on the map.
	Empty bool
	// Citable lists the blob ids that a line of Map or Journal names as its evidence. It is the
	// allow-list for the read_output tool (M6.6): a model may only ask for the raw output of a
	// command whose summary it was already shown, never for an arbitrary id.
	Citable map[int64]bool
}

// LoadAdviceContext reads the case and builds the context for tier 2.
func LoadAdviceContext(ctx context.Context, st *store.Store, opts ContextOptions) (AdviceContext, error) {
	opts = opts.withDefaults()
	e, err := engagement.Get(ctx, st)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return AdviceContext{}, err
	}
	view, err := mapview.Load(ctx, st)
	if err != nil {
		return AdviceContext{}, err
	}
	entries, err := st.Repo().ListJournal(ctx)
	if err != nil {
		return AdviceContext{}, err
	}
	sc := engagement.Scope(e)
	cites := newCitations()

	out := AdviceContext{
		Case:    caseBlock(e, sc.String()),
		Map:     mapText(view, opts, cites),
		Journal: journalText(entries, opts.JournalEntries, cites),
		Empty:   len(view.Hosts) == 0,
		Check:   advice.Context{Scope: sc, Hosts: map[string]bool{}, Hostnames: map[string]string{}, Services: map[string]bool{}},
		Citable: cites.ids,
	}
	for _, h := range view.Hosts {
		out.Check.Hosts[h.IP] = true
		if h.Hostname != "" {
			out.Check.Hostnames[strings.ToLower(h.Hostname)] = h.IP
		}
		for _, s := range h.Services {
			out.Check.Services[fmt.Sprintf("%s:%d/%s", h.IP, s.Port, s.Proto)] = true
		}
	}
	return out, nil
}

func caseBlock(e store.Engagement, scope string) string {
	line := func(k, v string) string {
		if v == "" {
			v = "(not set)"
		}
		return k + ": " + oneLine(v, 600) + "\n"
	}
	return line("target", e.Target) + line("goal", e.Goal) + line("scope", scope) + line("phase", e.Phase) + line("rules", e.ROE)
}

// mapText writes the map as dense text, most useful first: hosts with open services, then the
// rest. What does not fit is replaced by a line that says how much was left out. A host, service
// or finding line that has a known source is marked "(blob N)"; cites records every such id.
func mapText(v mapview.View, o ContextOptions, cites *citations) string {
	var blocks []string
	hosts := append([]mapview.Host(nil), v.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool { return openCount(hosts[i]) > openCount(hosts[j]) })
	for _, h := range hosts {
		var b strings.Builder
		fmt.Fprintf(&b, "host %s", clean(h.IP))
		if h.Hostname != "" {
			fmt.Fprintf(&b, " name=%s", oneLine(h.Hostname, 100))
		}
		if h.OS != "" {
			fmt.Fprintf(&b, " os=%s", oneLine(h.OS, 100))
		}
		fmt.Fprintf(&b, " %s", clean(h.Status))
		cites.mark(&b, h.SourceBlobID)
		b.WriteString("\n")
		closed := 0
		for _, s := range h.Services {
			if s.State != "open" {
				closed++
				continue
			}
			fmt.Fprintf(&b, "  %d/%s %s", s.Port, clean(s.Proto), oneLine(s.Name, 60))
			if s.Version != "" {
				fmt.Fprintf(&b, " %s", oneLine(s.Version, 160))
			}
			cites.mark(&b, s.SourceBlobID)
			b.WriteString("\n")
			shown := s.Paths
			if len(shown) > o.PathsPerService {
				shown = shown[:o.PathsPerService]
			}
			if len(shown) > 0 {
				var paths []string
				for _, p := range shown {
					item := oneLine(p.Path, 120)
					if p.StatusCode != nil {
						item += fmt.Sprintf(" %d", *p.StatusCode)
					}
					paths = append(paths, item)
				}
				fmt.Fprintf(&b, "    paths: %s", strings.Join(paths, ", "))
				if more := len(s.Paths) - len(shown); more > 0 {
					fmt.Fprintf(&b, " (+%d more)", more)
				}
				b.WriteString("\n")
			}
		}
		if closed > 0 {
			fmt.Fprintf(&b, "  (%d closed or filtered ports not listed)\n", closed)
		}
		blocks = append(blocks, b.String())
	}
	if len(v.Findings) > 0 {
		var b strings.Builder
		b.WriteString("findings:\n")
		for _, f := range v.Findings {
			fmt.Fprintf(&b, "  [%s] %s", clean(f.Severity), oneLine(f.Title, 200))
			cites.mark(&b, f.SourceBlobID)
			b.WriteString("\n")
		}
		blocks = append(blocks, b.String())
	}
	if len(v.Credentials) > 0 {
		var b strings.Builder
		b.WriteString("credentials found (secrets are never shown):\n")
		for _, c := range v.Credentials {
			fmt.Fprintf(&b, "  %s (%s) %s\n", oneLine(orDash(c.Username), 60), clean(c.Kind), oneLine(c.WorksOn, 100))
		}
		blocks = append(blocks, b.String())
	}
	return fit(blocks, o.MapBytes, "blocks of the map")
}

func openCount(h mapview.Host) int {
	n := 0
	for _, s := range h.Services {
		if s.State == "open" {
			n++
		}
	}
	return n
}

// journalText writes the journal: the latest entries whole, the older ones folded into counts and
// the first words of those the tester wrote. A tool's entry that has a known source is marked
// "(blob N)"; cites records every such id.
func journalText(entries []store.JournalEntry, keep int, cites *citations) string {
	if len(entries) == 0 {
		return "(empty)\n"
	}
	var b strings.Builder
	if len(entries) > keep {
		var older []store.JournalEntry
		older, entries = entries[:len(entries)-keep], entries[len(entries)-keep:]
		b.WriteString(foldJournal(older))
	}
	for _, e := range entries {
		who := "tool"
		if e.Author == "human" {
			who = "tester"
		}
		fmt.Fprintf(&b, "[%s] %s: %s", e.Time.UTC().Format("2006-01-02 15:04"), who, oneLine(e.Text, 400))
		cites.mark(&b, e.SourceBlobID)
		b.WriteString("\n")
	}
	return b.String()
}

// foldJournal replaces old entries by one summary. The tester's own words are kept short but not
// dropped: they are the part of the journal that no tool can rewrite.
func foldJournal(older []store.JournalEntry) string {
	var tester, tools int
	var words []string
	for _, e := range older {
		if e.Author == "human" {
			tester++
			if len(words) < 8 {
				words = append(words, oneLine(e.Text, 100))
			}
		} else {
			tools++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "(earlier: %d entries from %s to %s; %d by the tester, %d by tools)\n",
		len(older), older[0].Time.UTC().Format("2006-01-02 15:04"), older[len(older)-1].Time.UTC().Format("2006-01-02 15:04"), tester, tools)
	for _, w := range words {
		fmt.Fprintf(&b, "  tester wrote: %s\n", w)
	}
	return b.String()
}

// fit joins blocks while they fit in the limit, and says how many were left out.
func fit(blocks []string, limit int, what string) string {
	var b strings.Builder
	for i, blk := range blocks {
		if b.Len()+len(blk) > limit {
			fmt.Fprintf(&b, "[... %d of %d %s omitted to keep this short ...]\n", len(blocks)-i, len(blocks), what)
			return b.String()
		}
		b.WriteString(blk)
	}
	if b.Len() == 0 {
		return "(nothing is known yet)\n"
	}
	return b.String()
}

// citations collects the blob ids a piece of text cited as its evidence. It is how the
// read_output tool's allow-list (AdviceContext.Citable) is built: a model may ask for the raw
// output of a command only if a line it was shown already named that command's id.
type citations struct{ ids map[int64]bool }

func newCitations() *citations { return &citations{ids: map[int64]bool{}} }

// mark writes " (blob N)" to b and records N as citable, if id is set. A nil id (nothing is
// known about where a line came from) writes nothing.
func (c *citations) mark(b *strings.Builder, id *int64) {
	if id == nil {
		return
	}
	c.ids[*id] = true
	fmt.Fprintf(b, " (blob %d)", *id)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// clean removes control characters from a short, already-validated field (an address, a
// protocol, a status). It does no truncation: such fields are bounded by their own kind.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// oneLine makes text from the target fit on one line: control characters become spaces, runs
// of whitespace collapse to one, and it is cut at limit characters.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(clean(s)), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:max(limit-1, 0)]) + "…"
	}
	return s
}
