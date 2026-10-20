package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	t.Cleanup(func(v, c, d string) func() {
		return func() { version, commit, date = v, c, d }
	}(version, commit, date))
	version, commit, date = "1.2.3", "abc1234", "2026-01-02T03:04:05Z"

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	want := "kagegami 1.2.3 (commit abc1234, built 2026-01-02T03:04:05Z)\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunNotImplemented(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "Not yet implemented") {
		t.Errorf("stderr = %q, want a not-implemented message", stderr.String())
	}
}
