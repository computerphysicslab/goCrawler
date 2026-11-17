package stringlib

import "testing"

func TestRmNewLines(t *testing.T) {
	cases := map[string]string{
		"a\nb\nc":   "abc",
		"a\r\nb":    "a\rb",
		"no breaks": "no breaks",
		"":          "",
	}
	for in, want := range cases {
		if got := RmNewLines(in); got != want {
			t.Errorf("RmNewLines(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsNumeric(t *testing.T) {
	for _, in := range []string{"123", "0", "-7"} {
		if !IsNumeric(in) {
			t.Errorf("%q should be numeric", in)
		}
	}
	for _, in := range []string{"abc", "12a", ""} {
		if IsNumeric(in) {
			t.Errorf("%q should not be numeric", in)
		}
	}
}
