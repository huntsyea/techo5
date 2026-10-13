package api

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// entities is a cut of what the features register: one on the device itself, and one on each kind of
// sub-device, including a name both assistant slots share.
func entities() []esphome.Entity {
	return []esphome.Entity{
		&esphome.MediaPlayer{Base: esphome.Base{ObjectID: "speaker", Name: "Speaker"}},
		&esphome.Switch{Base: esphome.Base{ObjectID: "ring_muted", Name: "Ring while muted", DeviceID: component.DeviceRing}},
		&esphome.Number{Base: esphome.Base{ObjectID: "microphone_sensitivity", Name: "Room sensitivity", DeviceID: component.DeviceMicrophone}},
		&esphome.Number{Base: esphome.Base{ObjectID: "bass", Name: "Bass", DeviceID: component.DevicePlayback}},
		&esphome.Number{Base: esphome.Base{ObjectID: "wake_threshold_1", Name: "Wake word sensitivity", DeviceID: component.AssistantDevice(0)}},
		&esphome.Number{Base: esphome.Base{ObjectID: "wake_threshold_2", Name: "Wake word sensitivity", DeviceID: component.AssistantDevice(1)}},
	}
}

// What the device tells Home Assistant: the sub-devices, and each entity's device and name.
type seen struct {
	devices []esphome.Device
	placed  map[string]component.Placement
}

func told(devices []esphome.Device, ents []esphome.Entity) seen {
	d, err := component.Declared(ents)
	if err != nil {
		panic(err)
	}
	s := seen{devices: devices, placed: map[string]component.Placement{}}
	for _, p := range d {
		s.placed[p.ObjectID] = p
	}
	return s
}

// The sub-devices the API module announced before grouping could be turned off, word for word.
var todaysSubDevices = []esphome.Device{
	{ID: 1, Name: "Bedroom ring"},
	{ID: 2, Name: "Bedroom microphone"},
	{ID: 3, Name: "Bedroom playback"},
	{ID: 4, Name: "Bedroom assistant 1"},
	{ID: 5, Name: "Bedroom assistant 2"},
}

// device is a regrouping over fresh entities, with the setting and stage given saved in a fresh store,
// as a device starting up with them would have.
type device struct {
	*regrouping
	st      *config.Store
	ents    []esphome.Entity
	devices []esphome.Device
}

func start(t *testing.T, grouped bool, stage config.Stage) *device {
	t.Helper()
	st := store(t)
	if err := st.Set().Grouping().Grouped(grouped); err != nil {
		t.Fatal(err)
	}
	if err := st.Set().Grouping().Stage(stage); err != nil {
		t.Fatal(err)
	}
	return restart(t, st)
}

// store is a fresh settings file. Not the process-wide one: other packages' goroutines read that.
func store(t *testing.T) *config.Store {
	t.Helper()
	st, err := config.Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// restart is the daemon coming up again over whatever the store holds.
func restart(t *testing.T, st *config.Store) *device {
	t.Helper()
	d := &device{ents: entities(), st: st}
	r, a, err := newRegrouping(st, "Bedroom", d.ents)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	d.regrouping, d.devices = r, a.Devices
	return d
}

// served is what Run does between one serve and the next: put on whatever stage is due.
func (d *device) served() {
	if a, ok := d.between(); ok {
		d.devices = a.Devices
	}
}

func (d *device) now() seen { return told(d.devices, d.ents) }

// connected is Home Assistant connecting, reading the device and subscribing, then being dropped if
// the device asks for it.
func (d *device) connected() (reconnect bool) {
	reconnect = d.subscribed()
	if reconnect {
		d.served()
	}
	return reconnect
}

func names(s seen) map[string]string {
	out := map[string]string{}
	for id, p := range s.placed {
		out[id] = p.Name
	}
	return out
}

func devicesOf(s seen) map[string]uint32 {
	out := map[string]uint32{}
	for id, p := range s.placed {
		out[id] = p.DeviceID
	}
	return out
}

// With the default setting the device is exactly what it was: today's sub-devices, every entity where
// and as its feature declared it, and nothing to move however often Home Assistant connects.
func TestByDefaultTheDeviceIsAsItAlwaysWas(t *testing.T) {
	d := start(t, true, config.StageGrouped)
	want := told(nil, entities())

	for range 3 {
		if !slices.Equal(d.devices, todaysSubDevices) {
			t.Fatalf("sub-devices = %v, want %v", d.devices, todaysSubDevices)
		}
		if got := d.now(); !equalPlaced(got, want) {
			t.Fatalf("entities = %v, want them as declared", got.placed)
		}
		if d.connected() {
			t.Fatal("a device already where its setting wants it asked Home Assistant to reconnect")
		}
	}
	if s := d.st.Get().Grouping.Stage; s != config.StageGrouped {
		t.Errorf("saved stage = %s, want grouped", s)
	}
}

func equalPlaced(a, b seen) bool {
	if len(a.placed) != len(b.placed) {
		return false
	}
	for id, p := range a.placed {
		if b.placed[id] != p {
			return false
		}
	}
	return true
}

// Turned off, the first stage waits for Home Assistant to finish a connection at the stage it has, is
// saved before anything is announced, and is announced only once nothing is connected. A second
// subscribe before then, from another client or the same one, is not a second step.
func TestOneStepPerCompletedConnection(t *testing.T) {
	d := start(t, false, config.StageGrouped)
	before := d.now()

	if !d.subscribed() {
		t.Fatal("Home Assistant saw the grouped device and nothing moved on")
	}
	if s := d.st.Get().Grouping.Stage; s != config.StageRenamed {
		t.Fatalf("saved stage = %s, want renamed before the reconnect", s)
	}
	if !equalPlaced(d.now(), before) {
		t.Fatal("entities changed while Home Assistant could still be reading them")
	}
	if d.subscribed() {
		t.Fatal("a second subscribe at the same stage asked for a second step")
	}

	d.served()
	got := d.now()
	if !slices.Equal(got.devices, todaysSubDevices) {
		t.Errorf("renamed: sub-devices = %v, want them all still announced", got.devices)
	}
	if want := map[string]string{
		"speaker":                "Speaker",
		"ring_muted":             "Ring while muted",
		"microphone_sensitivity": "Microphone room sensitivity",
		"bass":                   "Bass",
		"wake_threshold_1":       "Assistant 1 wake word sensitivity",
		"wake_threshold_2":       "Assistant 2 wake word sensitivity",
	}; !maps.Equal(names(got), want) {
		t.Errorf("renamed: names = %v, want %v", names(got), want)
	}
	if !maps.Equal(devicesOf(got), devicesOf(before)) {
		t.Errorf("renamed: devices = %v, want unchanged %v", devicesOf(got), devicesOf(before))
	}

	if s := d.st.Get().Grouping.Stage; s != config.StageRenamed {
		t.Errorf("saved stage = %s after one step, want renamed", s)
	}
	d.served()
	if s := d.st.Get().Grouping.Stage; s != config.StageRenamed || !maps.Equal(names(d.now()), names(got)) {
		t.Error("serving again with no connection in between moved the device on")
	}
}

// flatNames are every entity's names from the renamed stage on.
var flatNames = map[string]string{
	"speaker":                "Speaker",
	"ring_muted":             "Ring while muted",
	"microphone_sensitivity": "Microphone room sensitivity",
	"bass":                   "Bass",
	"wake_threshold_1":       "Assistant 1 wake word sensitivity",
	"wake_threshold_2":       "Assistant 2 wake word sensitivity",
}

var groupedDevices = map[string]uint32{
	"speaker": 0, "ring_muted": 1, "microphone_sensitivity": 2, "bass": 3, "wake_threshold_1": 4, "wake_threshold_2": 5,
}

var flatDevices = map[string]uint32{
	"speaker": 0, "ring_muted": 0, "microphone_sensitivity": 0, "bass": 0, "wake_threshold_1": 0, "wake_threshold_2": 0,
}

// checkStage asserts the device announces what the spec says each stage is.
func checkStage(t *testing.T, d *device, s config.Stage) {
	t.Helper()
	got := d.now()
	wantDevices, wantPlaced, wantNames := todaysSubDevices, groupedDevices, flatNames
	switch s {
	case config.StageGrouped:
		wantNames = names(told(nil, entities()))
	case config.StageMoved:
		wantPlaced = flatDevices
	case config.StageFlat:
		wantDevices, wantPlaced = nil, flatDevices
	}
	if !slices.Equal(got.devices, wantDevices) {
		t.Errorf("%s: sub-devices = %v, want %v", s, got.devices, wantDevices)
	}
	if !maps.Equal(devicesOf(got), wantPlaced) {
		t.Errorf("%s: devices = %v, want %v", s, devicesOf(got), wantPlaced)
	}
	if !maps.Equal(names(got), wantNames) {
		t.Errorf("%s: names = %v, want %v", s, names(got), wantNames)
	}
	if saved := d.st.Get().Grouping.Stage; saved != s {
		t.Errorf("%s: saved stage = %s", s, saved)
	}
}

// Off, the device walks one stage per connection from grouped to flat, each as the spec describes it,
// and stops there: Home Assistant connecting to a flat device is not a reason to drop it.
func TestTurnedOffTheDeviceWalksToFlatAndStops(t *testing.T) {
	d := start(t, false, config.StageGrouped)
	checkStage(t, d, config.StageGrouped)
	for _, s := range config.Stages[1:] {
		if !d.connected() {
			t.Fatalf("no step toward %s", s)
		}
		checkStage(t, d, s)
	}
	for range 3 {
		if d.connected() {
			t.Fatal("a flat device turned off asked Home Assistant to reconnect")
		}
	}
	checkStage(t, d, config.StageFlat)
}

// A restart at any stage comes back announcing that stage, and waits for Home Assistant to see it
// before taking the next.
func TestARestartResumesAtTheSavedStage(t *testing.T) {
	for i, s := range config.Stages {
		t.Run(string(s), func(t *testing.T) {
			d := restart(t, start(t, false, s).st)
			checkStage(t, d, s)
			if d.served(); d.st.Get().Grouping.Stage != s {
				t.Fatal("a restarted device moved on before Home Assistant had seen it")
			}
			stepped := d.connected()
			if last := i == len(config.Stages)-1; stepped == last {
				t.Fatalf("stepped = %v at %s", stepped, s)
			}
			if i+1 < len(config.Stages) {
				checkStage(t, d, config.Stages[i+1])
			}
		})
	}
}

// A device that stopped after saving a step and before announcing it comes back at the saved one.
// Home Assistant had seen the stage before, so nothing is skipped.
func TestARestartBetweenSavingAndAnnouncingAnnouncesTheSavedStage(t *testing.T) {
	d := start(t, false, config.StageRenamed)
	if !d.subscribed() {
		t.Fatal("no step")
	}
	checkStage(t, restart(t, d.st), config.StageMoved)
}

// Turned back on, the same stages run the other way, from wherever the device is.
func TestTurnedBackOnTheDeviceWalksBackToGrouped(t *testing.T) {
	d := start(t, false, config.StageGrouped)
	d.connected()
	d.connected()
	checkStage(t, d, config.StageMoved)

	if err := d.st.Set().Grouping().Grouped(true); err != nil {
		t.Fatal(err)
	}
	for _, s := range []config.Stage{config.StageRenamed, config.StageGrouped} {
		if !d.connected() {
			t.Fatalf("no step back toward %s", s)
		}
		checkStage(t, d, s)
	}
	if d.connected() {
		t.Fatal("a grouped device turned on asked Home Assistant to reconnect")
	}
}

// api is the server around a device, for what the setup page calls.
func (d *device) api() *API {
	return &API{reconnect: make(chan struct{}, 1), regroup: d.regrouping, cfg: d.st}
}

func reconnected(a *API) bool {
	select {
	case <-a.reconnect:
		return true
	default:
		return false
	}
}

// Turning grouping off on a device Home Assistant is connected to takes the first step at once: Home
// Assistant has already seen the stage the device is at.
func TestTurningGroupingOffStepsAtOnceWhenHomeAssistantHasSeenTheDevice(t *testing.T) {
	d := start(t, true, config.StageGrouped)
	d.connected()
	a := d.api()

	if err := a.SetGrouped(false); err != nil {
		t.Fatal(err)
	}
	if d.st.Get().Grouping.Grouped {
		t.Fatal("the setting was not saved")
	}
	if !reconnected(a) {
		t.Fatal("Home Assistant was not dropped to see the first step")
	}
	d.served()
	checkStage(t, d, config.StageRenamed)
}

// Before Home Assistant has seen the current stage, whether since a restart or since the last step,
// a changed setting is saved and waits: stepping now could take Home Assistant past a stage unseen.
func TestAChangedSettingWaitsForHomeAssistantToSeeTheCurrentStage(t *testing.T) {
	d := start(t, true, config.StageGrouped)
	a := d.api()
	if err := a.SetGrouped(false); err != nil {
		t.Fatal(err)
	}
	if reconnected(a) || d.st.Get().Grouping.Stage != config.StageGrouped {
		t.Fatal("a device Home Assistant had not seen since starting stepped on a changed setting")
	}

	d.connected()
	checkStage(t, d, config.StageRenamed)
	if err := a.SetGrouped(false); err != nil {
		t.Fatal(err)
	}
	if reconnected(a) || d.st.Get().Grouping.Stage != config.StageRenamed {
		t.Fatal("a stage Home Assistant had not seen yet was stepped past")
	}
}

// Before the server has started there is nothing to step: the setting is saved, and the device
// starts at its saved stage and goes on from there.
func TestSettingGroupingBeforeTheServerStartsOnlySavesIt(t *testing.T) {
	st := store(t)
	a := &API{reconnect: make(chan struct{}, 1), cfg: st}
	if err := a.SetGrouped(false); err != nil {
		t.Fatal(err)
	}
	if g := st.Get().Grouping; g.Grouped || g.Stage != config.StageGrouped || reconnected(a) {
		t.Fatalf("grouping = %+v", g)
	}
}

// Two entities that would share a name once flattened stop the device at start-up, grouped or not.
func TestNamesThatWouldClashFlattenedStopTheStart(t *testing.T) {
	st := store(t)
	ents := append(entities(), &esphome.Number{Base: esphome.Base{ObjectID: "room_sensitivity", Name: "Microphone room sensitivity"}})
	if _, _, err := newRegrouping(st, "Bedroom", ents); err == nil {
		t.Fatal("a device whose flattened names clash started")
	}
}
