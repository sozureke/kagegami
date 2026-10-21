package tmux

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// ErrVersion means the output of tmux -V cannot be understood.
var ErrVersion = errors.New("unrecognized tmux version")

// VerifiedVersions are the tmux versions on which the behavior Kagegami depends on was
// actually observed: pipe-pane hands over raw OSC markers, pane ids and formats work as
// used here. Every entry has a source recorded in ADR-0007. A version that is not listed
// still works if the capture self-check passes, but is reported as unverified.
var VerifiedVersions = []Version{
	{Major: 3, Minor: 8}, // development machine, see ADR-0007
	{Major: 3, Minor: 4}, // GitHub Actions ubuntu-24.04 image, CI run 39582742565, see ADR-0007
}

// Version is a tmux version such as 3.3a. The letter suffix is kept but does not take
// part in comparisons.
type Version struct {
	Major, Minor int
	Suffix       string
}

func (v Version) String() string { return fmt.Sprintf("%d.%d%s", v.Major, v.Minor, v.Suffix) }

// AtLeast reports whether v is the same as or newer than other.
func (v Version) AtLeast(other Version) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	return v.Minor >= other.Minor
}

// Verified reports whether this major and minor version is in VerifiedVersions.
func (v Version) Verified() bool {
	return slices.ContainsFunc(VerifiedVersions, func(x Version) bool {
		return x.Major == v.Major && x.Minor == v.Minor
	})
}

// "tmux 3.8", "tmux 3.3a", "tmux next-3.5", "tmux openbsd-7.4".
var versionRE = regexp.MustCompile(`^tmux (?:[a-z]+-)?(\d+)\.(\d+)([a-z]?)\s*$`)

// ParseVersion reads the output of `tmux -V`.
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%w: %q", ErrVersion, s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return Version{Major: major, Minor: minor, Suffix: m[3]}, nil
}
