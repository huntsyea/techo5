package all

import (
	"fmt"
	"maps"
	"slices"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// haSim is what Home Assistant 2026.9.4's ESPHome integration does with the device registry and the
// entity registry each time the device connects, cut down to what decides whether an entity survives:
// unique ids, (device, key) slots, names, and which device each registry entry is on. It is written
// from the source, function by function, and each part says which one it copies. Anything that is not
// identity (states, subscriptions, entity objects, areas) is left out.
//
// Entity ids here are opaque ("switch.e12"): Home Assistant would make them from names, but what
// matters is only whether an entity keeps the one it had or is given another.
type haSim struct {
	mac string

	// devices is the sub-devices in the device registry, by ESPHome device id. The device itself is
	// always there and is device 0 below.
	devices map[uint32]bool

	// registry is the entity registry for this config entry, by unique id.
	registry map[string]*haEntry

	// deleted is the entity registry's deleted_entities, by unique id: what async_get_or_create restores
	// when an entity with that unique id comes back.
	deleted map[string]*haEntry

	// infos is RuntimeEntryData.info: the last entity list, per entity type, by slot. Home Assistant
	// keeps it in its store, so it survives a restart as well as a reconnect.
	infos map[string]*haInfos

	made int
}

// haEntry is one entity registry entry.
type haEntry struct {
	entityID string
	device   uint32
}

// haInfo is one EntityInfo as it arrives: the parts of it identity is made of.
type haInfo struct {
	objectID string
	typ      string
	deviceID uint32
	key      uint32
	name     string
}

// haSlot is DeviceEntityKey: (device_id, key).
type haSlot struct{ device, key uint32 }

// haInfos is one type's dict[DeviceEntityKey, EntityInfo], which in Python keeps insertion order. The
// order matters where a name has more than one candidate.
type haInfos struct {
	order []haSlot
	by    map[haSlot]haInfo
}

func newHAInfos() *haInfos { return &haInfos{by: map[haSlot]haInfo{}} }

func (d *haInfos) set(s haSlot, i haInfo) {
	if _, ok := d.by[s]; !ok {
		d.order = append(d.order, s)
	}
	d.by[s] = i
}

func (d *haInfos) pop(s haSlot) (haInfo, bool) {
	i, ok := d.by[s]
	if ok {
		delete(d.by, s)
		d.order = slices.DeleteFunc(d.order, func(o haSlot) bool { return o == s })
	}
	return i, ok
}

func (d *haInfos) values() []haInfo {
	out := make([]haInfo, 0, len(d.order))
	for _, s := range d.order {
		out = append(out, d.by[s])
	}
	return out
}

// haConnection is what one connection did to the entity registry.
type haConnection struct {
	created []string // unique ids an entry was created for, restored from deleted_entities or not
	removed []string // entity ids removed, by a sub-device going or by async_remove_entities
}

func newHASim(mac string) *haSim {
	return &haSim{
		mac:      mac,
		devices:  map[uint32]bool{},
		registry: map[string]*haEntry{},
		deleted:  map[string]*haEntry{},
		infos:    map[string]*haInfos{},
	}
}

// haUniqueID is aioesphomeapi's build_device_unique_id, which is build_unique_id at version 3:
// "{mac}/{device_id}/{entity_type}/{name}", entity_type being _TYPE_TO_NAME's, which Placement.Type is.
func haUniqueID(mac string, p component.Placement) string {
	return fmt.Sprintf("%s/%d/%s/%s", mac, p.DeviceID, p.Type, p.Name)
}

func (h *haSim) uniqueID(i haInfo) string {
	return haUniqueID(h.mac, component.Placement{Type: i.typ, DeviceID: i.deviceID, Name: i.name})
}

// connect is one connection: ESPHomeManager._on_connect, which calls _async_setup_device_registry and
// then entry_data.async_update_static_infos, which hands each type's infos to async_static_info_updated.
func (h *haSim) connect(a component.Announcement) haConnection {
	var c haConnection
	h.setupDeviceRegistry(a.Devices, &c)

	byType := map[string][]haInfo{}
	for _, p := range a.Entities {
		// The key is go-esphome-device's Base.Key, fnv1 of the object id, as ESPHome's own.
		key := (&esphome.Base{ObjectID: p.ObjectID}).Key()
		byType[p.Type] = append(byType[p.Type], haInfo{p.ObjectID, p.Type, p.DeviceID, key, p.Name})
	}
	// Types whose entities have all gone get an empty list, which removes them.
	for typ := range h.infos {
		if _, ok := byType[typ]; !ok {
			byType[typ] = nil
		}
	}
	for _, typ := range slices.Sorted(maps.Keys(byType)) {
		h.staticInfoUpdated(typ, byType[typ], &c)
	}
	return c
}

// setupDeviceRegistry is manager.py's _async_setup_device_registry, as far as sub-devices go.
func (h *haSim) setupDeviceRegistry(announced []esphome.Device, c *haConnection) {
	valid := map[uint32]bool{}
	for _, d := range announced {
		valid[d.ID] = true
	}
	// "Remove devices that no longer exist": every device of the entry matching neither the MAC
	// connection nor an announced "{mac}_{device_id}" identifier. Removing one fires
	// EVENT_DEVICE_REGISTRY_UPDATED, and the entity registry's async_device_modified (er.py) removes
	// every entity of the same config entry still on it. That happens before the entity list is looked
	// at, which is the whole reason for the stages.
	for _, id := range slices.Sorted(maps.Keys(h.devices)) {
		if valid[id] {
			continue
		}
		delete(h.devices, id)
		for uid, e := range h.registry {
			if e.device == id {
				h.remove(uid, c)
			}
		}
	}
	// "Create/update sub devices that should exist."
	for id := range valid {
		h.devices[id] = true
	}
}

// staticInfoUpdated is entity.py's async_static_info_updated for one entity type.
func (h *haSim) staticInfoUpdated(typ string, infos []haInfo, c *haConnection) {
	current := h.infos[typ]
	if current == nil {
		current = newHAInfos()
	}
	newInfos := map[haSlot]bool{}
	for _, i := range infos {
		newInfos[haSlot{i.deviceID, i.key}] = true
	}
	uniqueIDs := make([]string, len(infos))
	newUniqueIDs := map[string]bool{}
	for n, i := range infos {
		uniqueIDs[n] = h.uniqueID(i)
		newUniqueIDs[uniqueIDs[n]] = true
	}
	oldByUniqueID, movableByName := h.identityIndexes(current, newUniqueIDs)

	type pending struct {
		info     haInfo
		uniqueID string
	}
	var deferred []pending
	var add []haInfo

	// First pass: unique id matches and moves between devices.
	for n, info := range infos {
		uid := uniqueIDs[n]
		if old, ok := oldByUniqueID[uid]; ok {
			delete(oldByUniqueID, uid)
			current.pop(old)
			// A changed key is a rekey; the registry entry is untouched. Keys never change here.
			continue
		}

		// Name match: the entity moved between devices. Prefer a candidate whose slot has no incoming
		// info, since that slot's info is an in place rename of the candidate.
		name := nameOrObjectID(info)
		candidates := movableByName[name]
		if len(candidates) == 0 {
			deferred = append(deferred, pending{info, uid})
			continue
		}
		idx := slices.IndexFunc(candidates, func(s haSlot) bool { return !newInfos[s] })
		if idx == -1 {
			idx = 0
		}
		oldSlot := candidates[idx]
		movableByName[name] = slices.Delete(candidates, idx, idx+1)
		oldInfo, _ := current.pop(oldSlot)

		oldUID := h.uniqueID(oldInfo)
		entry, ok := h.registry[oldUID]
		if !ok {
			// "If entity not found in registry, re-add it. This happens when the device_id changed
			// and the old device was deleted."
			add = append(add, info)
			continue
		}
		if _, claimed := h.registry[uid]; oldUID != uid && claimed {
			// "Leave the entry untouched when the new unique_id is claimed."
		} else {
			if oldUID != uid {
				delete(h.registry, oldUID)
				h.registry[uid] = entry
			}
			// The new device is the sub-device by identifier, or the device itself by connection;
			// the entry is moved only if it is found.
			if info.deviceID == 0 || h.devices[info.deviceID] {
				entry.device = info.deviceID
			}
		}
		// The old entity is signalled away and a new one added, which finds the entry by unique id.
		add = append(add, info)
	}

	// Second pass: anything left at an incoming slot is a rename with a stable key; the registry entry
	// follows the new unique id (entry_data.py's async_migrate_unique_id). Everything else is new.
	for _, p := range deferred {
		renamed, ok := current.pop(haSlot{p.info.deviceID, p.info.key})
		if !ok {
			add = append(add, p.info)
			continue
		}
		h.migrateUniqueID(h.uniqueID(renamed), p.uniqueID)
	}

	// "Anything still in current_infos is now gone": entry_data's async_remove_entities removes each
	// from the registry by its old unique id.
	for _, gone := range current.values() {
		if _, ok := h.registry[h.uniqueID(gone)]; ok {
			h.remove(h.uniqueID(gone), c)
		}
	}

	next := newHAInfos()
	for _, i := range infos {
		next.set(haSlot{i.deviceID, i.key}, i)
	}
	h.infos[typ] = next

	for _, i := range add {
		h.getOrCreate(i, c)
	}
}

// identityIndexes is entity.py's _build_identity_indexes.
func (h *haSim) identityIndexes(current *haInfos, newUniqueIDs map[string]bool) (map[string]haSlot, map[string][]haSlot) {
	byUniqueID := map[string]haSlot{}
	movable := map[string][]haSlot{}
	for _, s := range current.order {
		info := current.by[s]
		uid := h.uniqueID(info)
		byUniqueID[uid] = s
		if !newUniqueIDs[uid] {
			movable[nameOrObjectID(info)] = append(movable[nameOrObjectID(info)], s)
		}
	}
	return byUniqueID, movable
}

func nameOrObjectID(i haInfo) string {
	if i.name != "" {
		return i.name
	}
	return i.objectID
}

// migrateUniqueID is entry_data.py's async_migrate_unique_id: move the entry to the new unique id
// unless that is already claimed.
func (h *haSim) migrateUniqueID(oldUID, newUID string) {
	e, ok := h.registry[oldUID]
	if oldUID == newUID || !ok {
		return
	}
	if _, claimed := h.registry[newUID]; claimed {
		return
	}
	delete(h.registry, oldUID)
	h.registry[newUID] = e
}

// getOrCreate is an entity being added to Home Assistant: the entity platform calls er.py's
// async_get_or_create with its unique id and device, which finds the entry, or restores one from
// deleted_entities, or makes a new one.
func (h *haSim) getOrCreate(i haInfo, c *haConnection) {
	uid := h.uniqueID(i)
	if e, ok := h.registry[uid]; ok {
		e.device = i.deviceID
		return
	}
	c.created = append(c.created, uid)
	if e, ok := h.deleted[uid]; ok {
		delete(h.deleted, uid)
		e.device = i.deviceID
		h.registry[uid] = e
		return
	}
	h.made++
	h.registry[uid] = &haEntry{entityID: fmt.Sprintf("%s.e%d", i.typ, h.made), device: i.deviceID}
}

// remove is er.py's async_remove, which keeps the entry in deleted_entities.
func (h *haSim) remove(uid string, c *haConnection) {
	e := h.registry[uid]
	delete(h.registry, uid)
	h.deleted[uid] = e
	c.removed = append(c.removed, e.entityID)
}
