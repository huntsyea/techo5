package voice

import "testing"

func TestAnnouncingShowsWhilePlayed(t *testing.T) {
	var a onAir
	fired := 0
	a.hook.Listen(func(struct{}) { fired++ })

	if id := a.begin("  "); id != 0 || fired != 0 {
		t.Fatalf("an announcement without words was shown (id %d, fired %d)", id, fired)
	}
	first := a.begin("Dinner's ready")
	if m, ok := a.showing(); !ok || m.Text != "Dinner's ready" || m.Since.IsZero() {
		t.Fatalf("showing %+v %v", m, ok)
	}
	second := a.begin("The washer is done")
	a.end(first) // the first one finishing must not take the second down
	if m, ok := a.showing(); !ok || m.Text != "The washer is done" {
		t.Fatalf("a finished announcement took the next one down: %+v %v", m, ok)
	}
	a.end(second)
	if _, ok := a.showing(); ok {
		t.Fatal("still showing after it ended")
	}
	a.end(second)
	if fired != 3 {
		t.Fatalf("changed fired %d times, want 3", fired)
	}
}
