package classifier

import "testing"

func TestSliceByUTF16(t *testing.T) {
	s := "a😊b" // emoji is 2 UTF-16 code units

	if got := SliceByUTF16(s, 0, 1); got != "a" {
		t.Fatalf("off=0 len=1: got %q", got)
	}
	if got := SliceByUTF16(s, 1, 2); got != "😊" {
		t.Fatalf("off=1 len=2: got %q", got)
	}
	if got := SliceByUTF16(s, 3, 1); got != "b" {
		t.Fatalf("off=3 len=1: got %q", got)
	}
}

