package recon

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// WebTarget is the place a web request goes to, as read from a URL.
type WebTarget struct {
	// IP is set when the URL names the host by address.
	IP string
	// Host is set when it names the host by name.
	Host  string
	Port  int
	Proto string // always tcp
	Path  string // as in the URL, "" for none
	TLS   bool
}

// ParseWebURL reads an http or https URL. It reports false for anything else.
func ParseWebURL(raw string) (WebTarget, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return WebTarget{}, false
	}
	t := WebTarget{Proto: "tcp", Path: u.Path, TLS: u.Scheme == "https", Port: 80}
	if t.TLS {
		t.Port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return WebTarget{}, false
		}
		t.Port = n
	}
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil {
		t.IP = addr.Unmap().String()
	} else {
		t.Host = strings.ToLower(u.Hostname())
	}
	return t, true
}

// FlagValue finds the value of an option in command-line arguments. Each name is tried as
// "-name value", "-name=value" and, for names starting with "--", "--name value".
func FlagValue(args []string, names ...string) string {
	for i, a := range args {
		for _, name := range names {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
			if v, ok := strings.CutPrefix(a, name+"="); ok {
				return v
			}
		}
	}
	return ""
}

// HasFlag reports whether one of the options is among the arguments.
func HasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, name := range names {
			if a == name || strings.HasPrefix(a, name+"=") {
				return true
			}
		}
	}
	return false
}

// PathDelta makes the delta for a path found on a web target; ok is false if the target does
// not say which host it is.
func (t WebTarget) PathDelta(path string, status *int, note string) WebPathDelta {
	return WebPathDelta{HostIP: t.IP, Host: t.Host, Port: t.Port, Proto: t.Proto, Path: path, StatusCode: status, Note: note}
}
