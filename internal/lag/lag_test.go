package lag

import (
	"testing"
	"time"
)

func TestIDTime(t *testing.T) {
	got, ok := idTime("1762991940123-4")
	if !ok || !got.Equal(time.UnixMilli(1762991940123).UTC()) {
		t.Errorf("idTime = %v %v", got, ok)
	}
	for _, id := range []string{"", "0-0", "abc-1", "-"} {
		if _, ok := idTime(id); ok {
			t.Errorf("idTime(%q) ok", id)
		}
	}
}

func TestEarliest(t *testing.T) {
	if got := earliest([]string{"2000-0", "", "1000-5", "x"}); !got.Equal(time.UnixMilli(1000).UTC()) {
		t.Errorf("earliest = %v", got)
	}
	if got := earliest([]string{"", "nope"}); !got.IsZero() {
		t.Errorf("earliest of nothing = %v", got)
	}
}
