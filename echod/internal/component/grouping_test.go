package component

import (
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// declared is a cut of what the features register, as they declare it: a few on the device itself, and
// some on each kind of sub-device, including the names both assistant slots share.
var declared = []Placement{
	{ObjectID: "speaker", DeviceID: 0, Name: "Speaker"},
	{ObjectID: "ring", DeviceID: DeviceRing, Name: "LED ring"},
	{ObjectID: "ring_muted", DeviceID: DeviceRing, Name: "Ring while muted"},
	{ObjectID: "mic_mute", DeviceID: DeviceMicrophone, Name: "Microphone mute"},
	{ObjectID: "microphone_sensitivity", DeviceID: DeviceMicrophone, Name: "Room sensitivity"},
	{ObjectID: "stop_word_sensitivity", DeviceID: DeviceMicrophone, Name: "Stop word sensitivity"},
	{ObjectID: "bass", DeviceID: DevicePlayback, Name: "Bass"},
	{ObjectID: "wake_assistant_1", DeviceID: AssistantDevice(0), Name: "Wake"},
	{ObjectID: "wake_threshold_1", DeviceID: AssistantDevice(0), Name: "Wake word sensitivity"},
	{ObjectID: "wake_assistant_2", DeviceID: AssistantDevice(1), Name: "Wake"},
	{ObjectID: "wake_threshold_2", DeviceID: AssistantDevice(1), Name: "Wake word sensitivity"},
}

// The sub-devices as the API module announced them before any of this existed, word for word.
var todaysSubDevices = []esphome.Device{
	{ID: 1, Name: "Echo Spot ring"},
	{ID: 2, Name: "Echo Spot microphone"},
	{ID: 3, Name: "Echo Spot playback"},
	{ID: 4, Name: "Echo Spot assistant 1"},
	{ID: 5, Name: "Echo Spot assistant 2"},
}

func announce(t *testing.T, s config.Stage) Announcement {
	t.Helper()
	a, err := Announce("Echo Spot", declared, s)
	if err != nil {
		t.Fatalf("Announce(%s): %v", s, err)
	}
	return a
}

// Grouped is the device as it has always been: every sub-device, and every entity where and as its
// feature declared it.
func TestGroupedIsTheDeviceAsDeclared(t *testing.T) {
	a := announce(t, config.StageGrouped)

	if !slices.Equal(a.Devices, todaysSubDevices) {
		t.Errorf("sub-devices = %v, want %v", a.Devices, todaysSubDevices)
	}
	if !slices.Equal(a.Entities, declared) {
		t.Errorf("entities = %v, want them as declared", a.Entities)
	}
}

// The names every entity has once flattened. The assistant slots' all say which assistant; elsewhere
// only a name that would be unclear on the device's own page says which part it belongs to.
var flatNames = map[string]string{
	"speaker":                "Speaker",
	"ring":                   "LED ring",
	"ring_muted":             "Ring while muted",
	"mic_mute":               "Microphone mute",
	"microphone_sensitivity": "Microphone room sensitivity",
	"stop_word_sensitivity":  "Stop word sensitivity",
	"bass":                   "Bass",
	"wake_assistant_1":       "Assistant 1 wake",
	"wake_threshold_1":       "Assistant 1 wake word sensitivity",
	"wake_assistant_2":       "Assistant 2 wake",
	"wake_threshold_2":       "Assistant 2 wake word sensitivity",
}

// Renamed keeps every entity in its slot, the same device and the same key, and changes only its name,
// which is the one change Home Assistant reads as a rename of the entity it already has.
func TestRenamedChangesOnlyNames(t *testing.T) {
	a := announce(t, config.StageRenamed)

	if !slices.Equal(a.Devices, todaysSubDevices) {
		t.Errorf("sub-devices = %v, want them all still announced", a.Devices)
	}
	for i, p := range a.Entities {
		was := declared[i]
		want := Placement{ObjectID: was.ObjectID, DeviceID: was.DeviceID, Name: flatNames[was.ObjectID]}
		if p != want {
			t.Errorf("%s = %+v, want %+v", was.ObjectID, p, want)
		}
	}
}

// Moved puts every entity on the device itself under exactly the name it took when renamed, which is
// how Home Assistant finds the entity it already has on the sub-device. The sub-devices are still
// announced, empty, because one that is not announced is removed with everything still on it.
func TestMovedKeepsTheRenamedNamesOnTheDeviceItself(t *testing.T) {
	a := announce(t, config.StageMoved)

	if !slices.Equal(a.Devices, todaysSubDevices) {
		t.Errorf("sub-devices = %v, want them all still announced", a.Devices)
	}
	for i, p := range a.Entities {
		was := declared[i]
		want := Placement{ObjectID: was.ObjectID, DeviceID: 0, Name: flatNames[was.ObjectID]}
		if p != want {
			t.Errorf("%s = %+v, want %+v", was.ObjectID, p, want)
		}
	}
}

// Flat is moved without the sub-devices, which Home Assistant then removes for being empty and no
// longer there.
func TestFlatAnnouncesNoSubDevices(t *testing.T) {
	a := announce(t, config.StageFlat)
	moved := announce(t, config.StageMoved)

	if len(a.Devices) != 0 {
		t.Errorf("sub-devices = %v, want none", a.Devices)
	}
	if !slices.Equal(a.Entities, moved.Entities) {
		t.Errorf("entities = %v, want them as at the moved stage", a.Entities)
	}
}

// A stage this build does not know announces nothing at all, rather than a guess.
func TestUnknownStageIsAnError(t *testing.T) {
	if _, err := Announce("Echo Spot", declared, "sideways"); err == nil {
		t.Error("an unknown stage was announced")
	}
}

// Two entities that would share a name on the device itself are refused at every stage, grouped too: a
// device that would only break once someone flattened it should not start at all. ESPHome requires
// names unique per device, and Home Assistant's entity ids come from them, so case does not tell two
// apart.
func TestAFlattenedClashIsRefusedAtEveryStage(t *testing.T) {
	clash := append(slices.Clone(declared), Placement{ObjectID: "wake_now", DeviceID: 0, Name: "assistant 1 Wake"})
	for _, s := range config.Stages {
		if _, err := Announce("Echo Spot", clash, s); err == nil {
			t.Errorf("%s: a clash with Assistant 1 wake was announced", s)
		}
	}
}

// A clash between two entities that stay on the same sub-device is refused just the same.
func TestAClashOnOneSubDeviceIsRefused(t *testing.T) {
	clash := append(slices.Clone(declared), Placement{ObjectID: "bass_2", DeviceID: DevicePlayback, Name: "Bass"})
	if _, err := Announce("Echo Spot", clash, config.StageGrouped); err == nil {
		t.Error("two entities called Bass on playback were announced")
	}
}

// An entity on a sub-device that does not exist has nowhere to go once flattened and no label to take.
func TestAnUnknownSubDeviceIsRefused(t *testing.T) {
	stray := append(slices.Clone(declared), Placement{ObjectID: "stray", DeviceID: 99, Name: "Stray"})
	if _, err := Announce("Echo Spot", stray, config.StageGrouped); err == nil {
		t.Error("an entity on sub-device 99 was announced")
	}
}

// Turning grouping off walks grouped, renamed, moved, flat and stops there; turning it back on walks the
// same steps back. Each step is one Home Assistant can match, and none is skipped.
func TestNextWalksOneStageTowardTheSetting(t *testing.T) {
	walk := func(grouped bool, from config.Stage) []config.Stage {
		got := []config.Stage{from}
		for s := from; ; {
			n := Next(grouped, s)
			if n == s {
				return got
			}
			got = append(got, n)
			s = n
		}
	}

	off := walk(false, config.StageGrouped)
	if want := []config.Stage{"grouped", "renamed", "moved", "flat"}; !slices.Equal(off, want) {
		t.Errorf("turning grouping off walks %v, want %v", off, want)
	}
	on := walk(true, config.StageFlat)
	if want := []config.Stage{"flat", "moved", "renamed", "grouped"}; !slices.Equal(on, want) {
		t.Errorf("turning grouping on walks %v, want %v", on, want)
	}
	back := walk(true, config.StageRenamed)
	if want := []config.Stage{"renamed", "grouped"}; !slices.Equal(back, want) {
		t.Errorf("turning grouping on halfway walks %v, want %v", back, want)
	}
}

// What features register is read as they declared it, with the type Home Assistant files each under, and
// an announcement put back onto the same entities changes where and as what they are announced and
// nothing else: the object id, and so the key, stay as they were.
func TestDeclaredAndApplyRoundTrip(t *testing.T) {
	sw := &esphome.Switch{Base: esphome.Base{ObjectID: "mic_mute", Name: "Microphone mute", DeviceID: DeviceMicrophone}}
	num := &esphome.Number{Base: esphome.Base{ObjectID: "wake_threshold_2", Name: "Wake word sensitivity", DeviceID: AssistantDevice(1)}}
	text := &esphome.TextSensor{Base: esphome.Base{ObjectID: "last_reply", Name: "Last reply"}}
	ents := []esphome.Entity{sw, num, text}
	key := num.Key()

	got, err := Declared(ents)
	if err != nil {
		t.Fatal(err)
	}
	want := []Placement{
		{ObjectID: "mic_mute", Type: "switch", DeviceID: 2, Name: "Microphone mute"},
		{ObjectID: "wake_threshold_2", Type: "number", DeviceID: 5, Name: "Wake word sensitivity"},
		{ObjectID: "last_reply", Type: "text_sensor", DeviceID: 0, Name: "Last reply"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("declared = %+v, want %+v", got, want)
	}

	a, err := Announce("Echo Spot", got, config.StageMoved)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ents, a); err != nil {
		t.Fatal(err)
	}
	if num.ObjectID != "wake_threshold_2" || num.Key() != key {
		t.Errorf("the object id or key changed: %q %d", num.ObjectID, num.Key())
	}
	if num.DeviceID != 0 || num.Name != "Assistant 2 wake word sensitivity" {
		t.Errorf("wake_threshold_2 is on %d as %q", num.DeviceID, num.Name)
	}
	if sw.DeviceID != 0 || sw.Name != "Microphone mute" {
		t.Errorf("mic_mute is on %d as %q", sw.DeviceID, sw.Name)
	}
}

// An announcement that does not cover every entity is refused rather than half applied, since what is
// left behind would be announced as it was at another stage.
func TestApplyRefusesAnAnnouncementForOtherEntities(t *testing.T) {
	ents := []esphome.Entity{&esphome.Switch{Base: esphome.Base{ObjectID: "a", Name: "A"}}}
	if err := Apply(ents, Announcement{}); err == nil {
		t.Error("an announcement without the entity was applied")
	}
}
