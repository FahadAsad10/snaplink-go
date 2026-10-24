package shortid

import (
	"strings"
	"testing"
)

func TestNewUsesAlphabetAndLength(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		code, err := New(7)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 7 {
			t.Fatalf("len = %d", len(code))
		}
		for _, c := range code {
			if !strings.ContainsRune(alphabet, c) {
				t.Fatalf("unexpected char %q", c)
			}
		}
		seen[code] = true
	}
	if len(seen) < 999 {
		t.Fatalf("too many collisions: %d unique of 1000", len(seen))
	}
}

func TestValid(t *testing.T) {
	for code, want := range map[string]bool{
		"launch": true, "my_link-2": true, "ab": false, "has space": false, "emoji🙂": false,
		strings.Repeat("a", 33): false,
	} {
		if got := Valid(code); got != want {
			t.Errorf("Valid(%q) = %v, want %v", code, got, want)
		}
	}
}
