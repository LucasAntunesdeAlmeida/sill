package term

import "testing"

func TestFromEnv(t *testing.T) {
	cases := map[string]int{"120": 120, "0": 0, "-5": 0, "wide": 0, "": 0}
	for in, want := range cases {
		if got := fromEnv(in); got != want {
			t.Errorf("fromEnv(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestWidthNeverNegative(t *testing.T) {
	// Whether or not a console is attached, the answer is 0 or a real width.
	if w := Width(); w < 0 {
		t.Errorf("Width() = %d", w)
	}
	if w, ok := consoleWidth(); ok && w <= 0 {
		t.Errorf("consoleWidth reported ok with width %d", w)
	}
}
