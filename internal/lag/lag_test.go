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
