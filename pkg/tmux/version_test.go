package tmux

import (
	"errors"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want Version
	}{
		{"tmux 3.8", Version{3, 8, ""}},
		{"tmux 3.3a", Version{3, 3, "a"}},
		{"tmux 3.5a", Version{3, 5, "a"}},
		{"tmux next-3.5", Version{3, 5, ""}},
		{"tmux openbsd-7.4", Version{7, 4, ""}},
		{"tmux 10.12", Version{10, 12, ""}},
		{"tmux 3.8\n", Version{3, 8, ""}},
		{"tmux 3.8  ", Version{3, 8, ""}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseVersion(tt.in)
			if err != nil || got != tt.want {
				t.Errorf("ParseVersion(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestParseVersionRejectsWhatItDoesNotUnderstand(t *testing.T) {
	for _, in := range []string{"", "tmux", "tmux master", "tmux 3", "tmux 3.", "tmux .8", "tmux 3.8.1", "tmux 3.8ab", "screen 4.09", "3.8", "tmux three.eight", "tmux -3.8", "tmux 3.8\nextra"} {
		if _, err := ParseVersion(in); !errors.Is(err, ErrVersion) {
			t.Errorf("ParseVersion(%q) err = %v, want ErrVersion", in, err)
		}
	}
}

func TestVersionString(t *testing.T) {
	for in, want := range map[Version]string{{3, 8, ""}: "3.8", {3, 3, "a"}: "3.3a", {10, 0, ""}: "10.0"} {
		if got := in.String(); got != want {
			t.Errorf("%+v.String() = %q, want %q", in, got, want)
		}
	}
}

func TestAtLeast(t *testing.T) {
	tests := []struct {
		v, other Version
		want     bool
	}{
		{Version{3, 8, ""}, Version{3, 8, ""}, true},
		{Version{3, 9, ""}, Version{3, 8, ""}, true},
		{Version{3, 7, ""}, Version{3, 8, ""}, false},
		{Version{4, 0, ""}, Version{3, 9, ""}, true},
		{Version{2, 9, ""}, Version{3, 0, ""}, false},
		{Version{3, 10, ""}, Version{3, 8, ""}, true}, // 3.10 is newer than 3.8, not older
		{Version{3, 3, "a"}, Version{3, 3, ""}, true}, // the suffix does not take part
		{Version{3, 3, ""}, Version{3, 3, "a"}, true},
	}
	for _, tt := range tests {
		if got := tt.v.AtLeast(tt.other); got != tt.want {
			t.Errorf("%v.AtLeast(%v) = %v, want %v", tt.v, tt.other, got, tt.want)
		}
	}
}

func TestVerified(t *testing.T) {
	old := VerifiedVersions
	t.Cleanup(func() { VerifiedVersions = old })
	VerifiedVersions = []Version{{3, 8, ""}, {3, 4, ""}}

	for v, want := range map[Version]bool{
		{3, 8, ""}:  true,
		{3, 8, "a"}: true, // the letter is a bug-fix release of the same version
		{3, 4, ""}:  true,
		{3, 5, ""}:  false, // between two verified versions: not verified
		{3, 3, "a"}: false,
		{4, 0, ""}:  false,
		{2, 8, ""}:  false,
	} {
		if got := v.Verified(); got != want {
			t.Errorf("%v.Verified() = %v, want %v", v, got, want)
		}
	}
}

func TestTheDevelopmentVersionIsVerified(t *testing.T) {
	if !(Version{Major: 3, Minor: 8}).Verified() {
		t.Error("3.8 is the version the behavior was observed on and must be listed")
	}
}
