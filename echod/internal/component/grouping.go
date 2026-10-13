package component

// Grouping: where each entity is announced to Home Assistant, grouped onto sub-devices or flattened onto
// the device itself, and the staged way between the two.
//
// Features declare their entities as they always have, on a sub-device (Base.DeviceID) under a name that
// assumes it. Nothing here asks them to change. The whole of grouping is done where entities reach the
// API server:
//
//	declared, err := component.Declared(component.Default().Entities()) // once, before any Apply
//	a, err := component.Announce(deviceName, declared, config.Get().Grouping.Stage)
//	info.Devices = a.Devices         // the sub-devices to announce at this stage
//	err = component.Apply(ents, a)   // each entity's DeviceID and Name, by object id
//	next := component.Next(config.Get().Grouping.Grouped, stage) // one step toward the setting
//
// Announce is pure: the same placements and stage give the same announcement, so a test or a manifest
// writer can ask what any stage announces without a server. It fails on anything that could not be
// announced at every stage, which makes it the start-up check for flattened names.
//
// Why there are stages (config.Grouping) is Home Assistant: it keeps an entity across a rename in the
// same (device, key) slot, or across a move to another device under the same name, but not both at
// once, and it removes a sub-device it is no longer told about together with whatever is still on it.
// So grouped goes to flat by renaming in place, then moving, then dropping the empty sub-devices, and
// back the same way. Object ids never change at any stage, so neither do keys.

import (
	"fmt"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Placement is where one entity is announced to Home Assistant: on which device, under which name.
// ObjectID says which entity, and never changes with either (the key is a hash of it).
type Placement struct {
	ObjectID string

	// Type is what Home Assistant files the entity under, as aioesphomeapi names it ("switch",
	// "text_sensor"): with the device and the name it makes the entity's unique id.
	Type string

	DeviceID uint32
	Name     string
}

// Announcement is what the device tells Home Assistant it is made of at one stage: the sub-devices to
// put in the device info, and every entity's placement, in the order given.
type Announcement struct {
	Devices  []esphome.Device
	Entities []Placement
}

// Announce is what to announce at a stage, for a device called name whose features declared the given
// placements.
//
// It refuses placements that could not be announced at every stage, whichever it is asked for: an
// entity on a sub-device that does not exist, two whose names would clash on one device, grouped or
// flattened, or a step between two stages that would give one entity's place in Home Assistant to
// another (matchable). So a clash fails the device at start-up wherever it is, rather than only once
// someone turns grouping off.
func Announce(name string, declared []Placement, stage config.Stage) (Announcement, error) {
	if !stage.Valid() {
		return Announcement{}, fmt.Errorf("grouping: unknown stage %q", stage)
	}
	for _, p := range declared {
		if _, ok := subDevice(p.DeviceID); p.DeviceID != 0 && !ok {
			return Announcement{}, fmt.Errorf("grouping: %s is on sub-device %d, which does not exist", p.ObjectID, p.DeviceID)
		}
	}
	for _, s := range []config.Stage{config.StageGrouped, config.StageFlat} {
		if err := unique(place(name, declared, s).Entities); err != nil {
			return Announcement{}, err
		}
	}
	for i := 1; i < len(config.Stages); i++ {
		from, to := config.Stages[i-1], config.Stages[i]
		a, b := place(name, declared, from).Entities, place(name, declared, to).Entities
		if err := matchable(from, to, a, b); err != nil {
			return Announcement{}, err
		}
		if err := matchable(to, from, b, a); err != nil {
			return Announcement{}, err
		}
	}
	return place(name, declared, stage), nil
}

// Next is the stage to announce after stage, one step toward what grouped asks for, or stage itself once
// it is there. It is only a step: when to take it, which is after Home Assistant has seen the current
// one, is the caller's to decide.
func Next(grouped bool, stage config.Stage) config.Stage {
	i := stage.Step()
	switch {
	case i < 0:
		return stage
	case grouped && i > 0:
		return config.Stages[i-1]
	case !grouped && i < len(config.Stages)-1:
		return config.Stages[i+1]
	}
	return stage
}

// place is Announce for placements already known to be sound.
func place(name string, declared []Placement, stage config.Stage) Announcement {
	var a Announcement
	if stage.AnnouncesSubDevices() {
		for _, d := range SubDevices() {
			a.Devices = append(a.Devices, esphome.Device{ID: d.ID, Name: name + " " + lowerFirst(d.Label)})
		}
	}

	for _, p := range declared {
		if d, ok := subDevice(p.DeviceID); ok {
			if stage.Renamed() {
				p.Name = d.flatName(p.ObjectID, p.Name)
			}
			if stage.Moved() {
				p.DeviceID = 0
			}
		}
		a.Entities = append(a.Entities, p)
	}
	return a
}

// unique refuses two entities with one name on one device. Case is ignored, since Home Assistant makes
// entity ids from names in lower case.
func unique(ents []Placement) error {
	type slot struct {
		device uint32
		name   string
	}
	seen := map[slot]string{}
	for _, p := range ents {
		k := slot{p.DeviceID, strings.ToLower(p.Name)}
		if other, dup := seen[k]; dup {
			return fmt.Errorf("grouping: %s and %s are both called %q on device %d", other, p.ObjectID, p.Name, p.DeviceID)
		}
		seen[k] = p.ObjectID
	}
	return nil
}

// matchable refuses a step from one stage to the next that Home Assistant would follow wrongly.
//
// Its first pass over an entity list (entity.py, async_static_info_updated) keeps every entity whose
// unique id it still sees, then matches each it does not know to one it no longer sees by name alone,
// within a type and across devices: that is how it follows a move. So an entity that takes a new name
// another entity of its type is giving up in the same step would be handed that other entity's registry
// entry, entity id and all. Unique names within each stage do not rule that out; only the pair does.
func matchable(fromStage, toStage config.Stage, from, to []Placement) error {
	type match struct{ typ, name string }
	uid := func(p Placement) string { return fmt.Sprintf("%d/%s/%s", p.DeviceID, p.Type, p.Name) }
	kept := map[string]bool{}
	for _, p := range to {
		kept[uid(p)] = true
	}
	known := map[string]bool{}
	gone := map[match][]string{}
	for _, p := range from {
		known[uid(p)] = true
		if !kept[uid(p)] {
			k := match{p.Type, p.Name}
			gone[k] = append(gone[k], p.ObjectID)
		}
	}
	for _, p := range to {
		if known[uid(p)] {
			continue
		}
		for _, other := range gone[match{p.Type, p.Name}] {
			if other != p.ObjectID {
				return fmt.Errorf("grouping: from %s to %s, %s would take %s's place in Home Assistant: it is called %q, as %s was",
					fromStage, toStage, p.ObjectID, other, p.Name, other)
			}
		}
	}
	return nil
}

func subDevice(id uint32) (SubDevice, bool) {
	for _, d := range SubDevices() {
		if d.ID == id {
			return d, true
		}
	}
	return SubDevice{}, false
}

// Declared is the placement every entity was registered with, which is what Announce starts from.
//
// Read it once, before anything is applied: Apply writes over the very fields this reads.
func Declared(ents []esphome.Entity) ([]Placement, error) {
	out := make([]Placement, 0, len(ents))
	for _, e := range ents {
		b, typ := base(e)
		if b == nil {
			return nil, fmt.Errorf("grouping: %s is a %T, which has no placement", e.Object(), e)
		}
		out = append(out, Placement{ObjectID: b.ObjectID, Type: typ, DeviceID: b.DeviceID, Name: b.Name})
	}
	return out, nil
}

// Apply puts an announcement's placements onto the entities, by object id. Nothing reaches Home
// Assistant until it next reads the entity list, which is once per connection.
//
// Every entity has to be in the announcement. One that is missing is refused before anything is
// changed, rather than left announced as it was at some other stage.
func Apply(ents []esphome.Entity, a Announcement) error {
	by := make(map[string]Placement, len(a.Entities))
	for _, p := range a.Entities {
		by[p.ObjectID] = p
	}
	for _, e := range ents {
		b, _ := base(e)
		if _, ok := by[e.Object()]; b == nil || !ok {
			return fmt.Errorf("grouping: nothing to announce %s as", e.Object())
		}
	}
	for _, e := range ents {
		b, _ := base(e)
		p := by[e.Object()]
		// The library has no setter for these and no lock over them (Base.mu guards only its notifier),
		// so they are written bare. That is safe as it is used: the API applies a stage only while no
		// client is connected. Name is read only when a connection lists entities, so nothing reads it
		// then. DeviceID is also read by a state change published meanwhile, which goes to no one; it is
		// an aligned 32-bit word, so that read sees the old device or the new, never a mix.
		b.DeviceID, b.Name = p.DeviceID, p.Name
	}
	return nil
}

// base is an entity's shared fields and its type as Home Assistant names it, or nil for an entity of a
// kind this does not know.
func base(e esphome.Entity) (*esphome.Base, string) {
	switch v := e.(type) {
	case *esphome.BinarySensor:
		return &v.Base, "binary_sensor"
	case *esphome.Button:
		return &v.Base, "button"
	case *esphome.Event:
		return &v.Base, "event"
	case *esphome.Light:
		return &v.Base, "light"
	case *esphome.MediaPlayer:
		return &v.Base, "media_player"
	case *esphome.Number:
		return &v.Base, "number"
	case *esphome.Select:
		return &v.Base, "select"
	case *esphome.Sensor:
		return &v.Base, "sensor"
	case *esphome.Switch:
		return &v.Base, "switch"
	case *esphome.TextSensor:
		return &v.Base, "text_sensor"
	case *esphome.Update:
		return &v.Base, "update"
	}
	return nil, ""
}

// lowerFirst is a label or a name moved from the front of a phrase into it. A leading acronym ("LED
// ring") keeps its capitals.
func lowerFirst(s string) string {
	if len(s) > 1 && strings.ToUpper(s[1:2]) == s[1:2] && strings.ToLower(s[1:2]) != s[1:2] {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
