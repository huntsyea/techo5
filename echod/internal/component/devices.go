package component

import (
	"fmt"
	"slices"
)

// Sub-devices Home Assistant shows beneath this one, which entities join by Base.DeviceID. Zero is the
// device itself and is what most things want.
//
// The point is a shorter entity list per page, not grouping for its own sake: Home Assistant puts every
// entity of a device on one page, and there are enough of them here to be unreadable. Each of these is a
// real entry in the device registry, linked back to the device, so one is worth having only where it holds
// enough entities to be worth clicking into.
//
// Names are composed with the device's own at runtime. Home Assistant uses what it is given verbatim, so a
// bare "Ring" would be a device called "Ring" in a house with several satellites.
//
// The numbers are identity: Home Assistant keys a sub-device on the address and this, so changing one
// orphans the entry it used to name. Add above DeviceAssistant, which has to stay last because the slots
// count upwards from it.
const (
	DeviceRing uint32 = iota + 1

	// DeviceMicrophone is the array: how it is combined, how hard it is driven, whether it is cut.
	DeviceMicrophone

	// DevicePlayback is how sound comes out, which is not the media player itself — that is the thing
	// people reach for, and it stays where it is found.
	DevicePlayback

	// DeviceAssistant is the first of one per wake word slot. Home Assistant keeps its own Assistant and
	// Wake word selects on the device itself, so choosing a wake word and tuning it are on different pages.
	DeviceAssistant
)

// AssistantSlots is how many assistants there are, and so how many assistant sub-devices: one per wake
// word Home Assistant offers at once (wakeword.Slots).
const AssistantSlots = 2

// AssistantDevice is the sub-device holding one slot's settings.
func AssistantDevice(slot int) uint32 { return DeviceAssistant + uint32(slot) }

// SubDevice is one sub-device: its identity and the short label that names it.
//
// The label is the one name it has. Grouped, it follows the device's own name to make the sub-device's
// ("Echo Spot ring"); flattened, it leads the names of the entities that would be unclear without it
// ("Assistant 1 wake word sensitivity").
type SubDevice struct {
	ID    uint32
	Label string

	// labelAll puts the label in front of every one of its entities once flattened, which the assistant
	// slots need because both declare the same names. Elsewhere only the names in labelled take it:
	// those that would read as something else beside everything on the device itself.
	labelAll bool
	labelled []string
}

// SubDevices is every sub-device, in the order they are announced.
func SubDevices() []SubDevice {
	out := []SubDevice{
		{ID: DeviceRing, Label: "Ring"},
		// The room's sound as the array hears it, which beside the room's light and the ring following the
		// room reads as something else entirely.
		{ID: DeviceMicrophone, Label: "Microphone", labelled: []string{"microphone_sensitivity", "room_floor", "room_level"}},
		{ID: DevicePlayback, Label: "Playback"},
	}
	for slot := range AssistantSlots {
		out = append(out, SubDevice{ID: AssistantDevice(slot), Label: fmt.Sprintf("Assistant %d", slot+1), labelAll: true})
	}
	return out
}

// flatName is what an entity on this sub-device is called on the device itself.
func (d SubDevice) flatName(objectID, name string) string {
	if d.labelAll || slices.Contains(d.labelled, objectID) {
		return d.Label + " " + lowerFirst(name)
	}
	return name
}
