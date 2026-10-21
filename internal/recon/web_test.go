package recon

import (
	"reflect"
	"testing"
)

func TestParseWebURL(t *testing.T) {
	tests := map[string]WebTarget{
		"http://10.0.0.1":           {IP: "10.0.0.1", Port: 80, Proto: "tcp"},
		"http://10.0.0.1/a/b":       {IP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/a/b"},
		"https://10.0.0.1":          {IP: "10.0.0.1", Port: 443, Proto: "tcp", TLS: true},
		"http://10.0.0.1:8080/":     {IP: "10.0.0.1", Port: 8080, Proto: "tcp", Path: "/"},
		"http://Box.HTB:81/x?y=1#z": {Host: "box.htb", Port: 81, Proto: "tcp", Path: "/x"},
		"http://[::1]:9000/":        {IP: "::1", Port: 9000, Proto: "tcp", Path: "/"},
		"http://[::ffff:10.0.0.1]/": {IP: "10.0.0.1", Port: 80, Proto: "tcp", Path: "/"},
		" http://10.0.0.1 ":         {IP: "10.0.0.1", Port: 80, Proto: "tcp"},
	}
	for raw, want := range tests {
		got, ok := ParseWebURL(raw)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseWebURL(%q) = %+v, %v; want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "ftp://x/", "10.0.0.1", "//x", "http://", "http://x:0/", "http://x:65536/", "http://x:abc/", "javascript:alert(1)", "http://%zz/"} {
		if got, ok := ParseWebURL(raw); ok {
			t.Errorf("ParseWebURL(%q) = %+v, want a refusal", raw, got)
		}
	}
}

func TestFlagValue(t *testing.T) {
	args := []string{"dir", "-u", "http://x", "--wordlist=/w", "-q", "-last"}
	for _, tt := range []struct {
		names []string
		want  string
	}{
		{[]string{"-u", "--url"}, "http://x"},
		{[]string{"--wordlist"}, "/w"},
		{[]string{"-w"}, ""},
		{[]string{"-last"}, ""}, // a flag at the end has no value
		{[]string{"-q"}, "-last"},
	} {
		if got := FlagValue(args, tt.names...); got != tt.want {
			t.Errorf("FlagValue(%v) = %q, want %q", tt.names, got, tt.want)
		}
	}
	if !HasFlag(args, "-q") || !HasFlag(args, "--wordlist") || HasFlag(args, "-x", "--nope") {
		t.Error("HasFlag")
	}
}

func TestPathDelta(t *testing.T) {
	code := 200
	got := WebTarget{Host: "box.htb", Port: 81, Proto: "tcp"}.PathDelta("/a", &code, "n")
	want := WebPathDelta{Host: "box.htb", Port: 81, Proto: "tcp", Path: "/a", StatusCode: &code, Note: "n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PathDelta = %+v", got)
	}
}
