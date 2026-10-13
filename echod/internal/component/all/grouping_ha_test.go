package all

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The migration is only as good as what Home Assistant makes of it. These walk the real entities through
// the stages against haSim, a copy of what Home Assistant 2026.9.4 does with each connection's device
// info and entity list, and check that every entity comes out the other end as the one it went in as.

const simMAC = "f0:81:73:10:53:58"

// announce is what the real entities announce at a stage.
func announce(t *testing.T, s config.Stage) component.Announcement {
	t.Helper()
	declared, err := component.Declared(component.Default().Entities())
	if err != nil {
		t.Fatal(err)
	}
	a, err := component.Announce("Echo Spot", declared, s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// walk connects a Home Assistant that has known the device at from, then at each of stages in turn,
// and returns it with the entity id every entity had at from, by object id.
func walk(t *testing.T, from config.Stage, stages []config.Stage) (*haSim, map[string]string, []haConnection) {
	t.Helper()
	h := newHASim(simMAC)
	first := announce(t, from)
	h.connect(first)
	before := entityIDs(t, h, first)

	var conns []haConnection
	for _, s := range stages {
		conns = append(conns, h.connect(announce(t, s)))
	}
	return h, before, conns
}

// entityIDs is the entity id of every announced entity, found the way anything outside Home Assistant
// finds it: by the unique id its announcement makes.
func entityIDs(t *testing.T, h *haSim, a component.Announcement) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range a.Entities {
		e, ok := h.registry[haUniqueID(simMAC, p)]
		if !ok {
			t.Errorf("%s: nothing in the registry under %q", p.ObjectID, haUniqueID(simMAC, p))
			continue
		}
		out[p.ObjectID] = e.entityID
	}
	return out
}

// lossless checks that a walk lost nothing: every entity keeps its entity id at every stage, nothing is
// created or removed along the way, and the registry holds exactly what is announced.
func lossless(t *testing.T, from config.Stage, stages []config.Stage) *haSim {
	t.Helper()
	h, before, conns := walk(t, from, stages)
	if len(before) == 0 {
		t.Fatal("no entities")
	}
	for i, s := range stages {
		c := conns[i]
		if len(c.created) > 0 {
			t.Errorf("%s: %d entities created in the registry, e.g. %v", s, len(c.created), first(c.created))
		}
		if len(c.removed) > 0 {
			t.Errorf("%s: %d entities removed from the registry, e.g. %v", s, len(c.removed), first(c.removed))
		}
	}
	last := announce(t, stages[len(stages)-1])
	after := entityIDs(t, h, last)
	for id, was := range before {
		if after[id] != was {
			t.Errorf("%s was %s and is now %q", id, was, after[id])
		}
	}
	if len(h.registry) != len(last.Entities) {
		t.Errorf("%d entries in the registry for %d entities", len(h.registry), len(last.Entities))
	}
	return h
}

func first(s []string) []string { return s[:min(3, len(s))] }

func TestHomeAssistantKeepsEveryEntityFromGroupedToFlat(t *testing.T) {
	h := lossless(t, config.StageGrouped, config.Stages[1:])
	if len(h.devices) > 0 {
		t.Errorf("sub-devices left at flat: %v", h.devices)
	}
}

func TestHomeAssistantKeepsEveryEntityFromFlatToGrouped(t *testing.T) {
	back := slices.Clone(config.Stages)
	slices.Reverse(back)
	h := lossless(t, config.StageFlat, back[1:])
	if len(h.devices) != len(component.SubDevices()) {
		t.Errorf("%d sub-devices back at grouped, want %d", len(h.devices), len(component.SubDevices()))
	}
}

// The controls: two ways of going flat that do lose entities, which the simulation has to catch for the
// two tests above to mean anything.

// recreated is the object ids a connection made new registry entries for.
func recreated(t *testing.T, c haConnection, a component.Announcement) []string {
	t.Helper()
	var out []string
	for _, p := range a.Entities {
		if slices.Contains(c.created, haUniqueID(simMAC, p)) {
			out = append(out, p.ObjectID)
		}
	}
	slices.Sort(out)
	return out
}

// Straight from grouped to flat, the sub-devices go first and take their entities with them, so every
// entity that was on one comes back as a new one.
func TestHomeAssistantRecreatesEverythingGroupedWhenGoingFlatInOneStep(t *testing.T) {
	grouped := announce(t, config.StageGrouped)
	var want []string
	for _, p := range grouped.Entities {
		if p.DeviceID != 0 {
			want = append(want, p.ObjectID)
		}
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("nothing is on a sub-device")
	}

	h, before, conns := walk(t, config.StageGrouped, []config.Stage{config.StageFlat})
	flat := announce(t, config.StageFlat)
	if got := recreated(t, conns[0], flat); !slices.Equal(got, want) {
		t.Errorf("recreated %v, want %v", got, want)
	}
	after := entityIDs(t, h, flat)
	for _, id := range want {
		if after[id] == before[id] {
			t.Errorf("%s kept %s", id, before[id])
		}
	}
}

// Moving without renaming first: an entity whose name changes both changes device and name at once,
// which Home Assistant cannot follow, so exactly those are new. Those whose name stays move by name.
func TestHomeAssistantRecreatesTheRenamedWhenTheRenameIsSkipped(t *testing.T) {
	grouped := announce(t, config.StageGrouped)
	moved := announce(t, config.StageMoved)
	var want []string
	for i, p := range grouped.Entities {
		if p.Name != moved.Entities[i].Name {
			want = append(want, p.ObjectID)
		}
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("no name changes when flattened")
	}

	_, _, conns := walk(t, config.StageGrouped, []config.Stage{config.StageMoved})
	if got := recreated(t, conns[0], moved); !slices.Equal(got, want) {
		t.Errorf("recreated %v, want %v", got, want)
	}
	if len(conns[0].removed) != len(want) {
		t.Errorf("%d removed, want the %d renamed", len(conns[0].removed), len(want))
	}
}

// At every stage, forwards and back, each entity's registry entry is under
// "{mac}/{device_id}/{type}/{name}" as announced at that stage, which is what the manifest tells anything
// outside Home Assistant to look it up by, and it is the entry the entity had to begin with.
func TestHomeAssistantUniqueIDsAtEveryStage(t *testing.T) {
	back := slices.Clone(config.Stages)
	slices.Reverse(back)
	path := append(slices.Clone(config.Stages), back[1:]...)

	h := newHASim(simMAC)
	h.connect(announce(t, path[0]))
	want := entityIDs(t, h, announce(t, path[0]))
	for _, s := range path[1:] {
		a := announce(t, s)
		h.connect(a)
		for _, p := range a.Entities {
			if e, ok := h.registry[haUniqueID(simMAC, p)]; !ok || e.entityID != want[p.ObjectID] {
				t.Errorf("%s: %s is not %s's entry", s, haUniqueID(simMAC, p), p.ObjectID)
			}
		}
	}

	// Worked through by hand from the declarations, so as not to trust haUniqueID alone.
	literal := map[config.Stage]map[string]string{
		config.StageGrouped: {
			"wake_threshold_1": simMAC + "/4/number/Wake word sensitivity",
			"room_level":       simMAC + "/2/sensor/Room level",
			"bass":             simMAC + "/3/number/Bass",
		},
		config.StageRenamed: {
			"wake_threshold_1": simMAC + "/4/number/Assistant 1 wake word sensitivity",
			"room_level":       simMAC + "/2/sensor/Microphone room level",
			"bass":             simMAC + "/3/number/Bass",
		},
		config.StageMoved: {
			"wake_threshold_1": simMAC + "/0/number/Assistant 1 wake word sensitivity",
			"room_level":       simMAC + "/0/sensor/Microphone room level",
			"bass":             simMAC + "/0/number/Bass",
		},
		config.StageFlat: {
			"wake_threshold_1": simMAC + "/0/number/Assistant 1 wake word sensitivity",
			"room_level":       simMAC + "/0/sensor/Microphone room level",
			"bass":             simMAC + "/0/number/Bass",
		},
	}
	for s, ids := range literal {
		found := 0
		for _, p := range announce(t, s).Entities {
			uid, ok := ids[p.ObjectID]
			if !ok {
				continue
			}
			found++
			if haUniqueID(simMAC, p) != uid {
				t.Errorf("%s: %s is %q, want %q", s, p.ObjectID, haUniqueID(simMAC, p), uid)
			}
		}
		if found != len(ids) {
			t.Errorf("%s: %d of %d entities found", s, found, len(ids))
		}
	}
}
