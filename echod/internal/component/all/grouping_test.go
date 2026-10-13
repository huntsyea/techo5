package all

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Everything this build registers can be announced at every stage, with no two names clashing on any
// device, grouped or flattened. This is the start-up check run ahead of time, once per build tag, so a
// clash a new entity brings is a failed test rather than a device that will not start.
func TestTheRealEntitiesAnnounceAtEveryStage(t *testing.T) {
	declared, err := component.Declared(component.Default().Entities())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range config.Stages {
		if _, err := component.Announce("Echo Spot", declared, s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

// What a few of the real entities are called once flattened: every assistant setting says which
// assistant, the microphone's room readings say they are the microphone's, and a name that already
// says what it belongs to is left alone.
func TestTheRealFlattenedNames(t *testing.T) {
	declared, err := component.Declared(component.Default().Entities())
	if err != nil {
		t.Fatal(err)
	}
	a, err := component.Announce("Echo Spot", declared, config.StageFlat)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range a.Entities {
		got[p.ObjectID] = p.Name
	}

	for id, want := range map[string]string{
		"wake_assistant_1":       "Assistant 1 wake",
		"wake_threshold_1":       "Assistant 1 wake word sensitivity",
		"follow_up_2":            "Assistant 2 follow-up time",
		"keep_recordings_2":      "Assistant 2 recordings kept",
		"room_floor":             "Microphone room floor",
		"room_level":             "Microphone room level",
		"microphone_sensitivity": "Microphone room sensitivity",
		"mic_mute":               "Microphone mute",
		"stop_word_sensitivity":  "Stop word sensitivity",
		"ring":                   "LED ring",
		"segment_1":              "LED ring segment 1",
		"ring_muted":             "Ring while muted",
		"bass":                   "Bass",
		"media_duck_level":       "Music ducking",
	} {
		if got[id] != want {
			t.Errorf("%s is %q flattened, want %q", id, got[id], want)
		}
	}
}
