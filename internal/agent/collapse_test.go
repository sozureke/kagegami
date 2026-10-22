package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/pkg/llm/llmtest"
)

func closedLines(from, to int) string {
	var b strings.Builder
	for p := from; p < to; p++ {
		fmt.Fprintf(&b, "[-] 10.10.11.5:%d/tcp closed (conn refused)\n", p)
	}
	return b.String()
}

func TestCollapseFoldsALongRunAndKeepsTheLineThatIsDifferent(t *testing.T) {
	in := "start\n" + closedLines(1000, 1300) + "[+] 10.10.11.5:6379/tcp OPEN redis 6.0.16 auth=none\n" + closedLines(1300, 1600) + "done\n"
	out := collapse(in)
	for _, want := range []string{"start", "[-] 10.10.11.5:1000/tcp", "[-] 10.10.11.5:1002/tcp", "[+] 10.10.11.5:6379/tcp OPEN redis 6.0.16 auth=none", "[-] 10.10.11.5:1599/tcp", "done", "[... 296 similar lines omitted ...]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the folded output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ":1003/tcp") || strings.Contains(out, ":1298/tcp") {
		t.Error("lines inside a run were kept")
	}
	if len(out) > len(in)/20 {
		t.Errorf("%d bytes became %d: not folded enough", len(in), len(out))
	}
}

func TestCollapseLeavesShortRunsAndVariedLinesAlone(t *testing.T) {
	short := closedLines(1, 1+maxRun)
	if collapse(short) != short {
		t.Error("a run of maxRun lines must stay as it is")
	}
	over := closedLines(1, 2+maxRun)
	if got := collapse(over); !strings.Contains(got, "[... 3 similar lines omitted ...]") {
		t.Errorf("a run of maxRun+1 lines was not folded:\n%s", got)
	}
	var varied strings.Builder
	for i := range 50 {
		varied.WriteString("service " + words(i) + " open\n")
	}
	if collapse(varied.String()) != varied.String() {
		t.Error("lines that differ in a word are not a run")
	}
	blank := strings.Repeat("\n", 30)
	if collapse(blank) != blank {
		t.Error("blank lines are not folded together")
	}
}

func TestCollapseNeverMakesTheTextLongerAndAccountsForEveryLine(t *testing.T) {
	// a run of very short lines: the note would be longer than what it replaces
	in := strings.Repeat("1\n", 20)
	if got := collapse(in); got != in {
		t.Errorf("collapse made %q out of %q", got, in)
	}
	long := closedLines(1, 101)
	out := collapse(long)
	kept := strings.Count(out, "[-] ")
	if kept+96 != 100 || !strings.Contains(out, "[... 96 similar lines omitted ...]") {
		t.Errorf("kept %d lines:\n%s", kept, out)
	}
}

func FuzzCollapse(f *testing.F) {
	f.Add("a 1\na 2\na 3\na 4\na 5\na 6\na 7\na 8\n")
	f.Add("\n\n\n")
	f.Add(closedLines(1, 40))
	f.Fuzz(func(t *testing.T, in string) {
		out := collapse(in)
		if len(out) > len(in) {
			t.Fatalf("%d bytes became %d", len(in), len(out))
		}
		if strings.Count(out, "\n") > strings.Count(in, "\n") {
			t.Fatal("lines were added")
		}
	})
}

func TestAnOutputThatFitsIsSentWhole(t *testing.T) {
	out := closedLines(1, 40) // many lines of one shape, but well within the room
	f := llmtest.NewFake(llmtest.Text(`{}`))
	if _, err := tier1(f, Tier1Options{Head: 6 << 10, Tail: 2 << 10}).Extract(bg, "cmd", 0, out); err != nil {
		t.Fatal(err)
	}
	msg := f.Requests()[0].Messages[0].Content
	if strings.Contains(msg, "similar lines") || !strings.Contains(msg, ":20/tcp") {
		t.Errorf("a short output was changed:\n%s", msg)
	}
}

func TestALongLogShowsTheModelItsKeyLine(t *testing.T) {
	key := "[+] 10.10.11.5:6379/tcp OPEN redis 6.0.16 auth=none"
	out := closedLines(1000, 1300) + key + "\n" + closedLines(1300, 1600)
	opts := DefaultTier1Options()
	if i := strings.Index(out, key); i <= opts.Head || len(out)-i <= opts.Tail {
		t.Fatalf("the key line is at %d of %d: the test would not show anything", i, len(out))
	}
	f := llmtest.NewFake(llmtest.Text(`{}`))
	if _, err := tier1(f, opts).Extract(bg, "probescan 10.10.11.5", 0, out); err != nil {
		t.Fatal(err)
	}
	msg := f.Requests()[0].Messages[0].Content
	if !strings.Contains(msg, key) {
		t.Errorf("the model never saw the key line:\n%s", msg)
	}
	if len(msg) > 2<<10 {
		t.Errorf("the message is %d bytes for an output that folds to a few lines", len(msg))
	}
}
