// Package manifest publishes what the device is announcing to Home Assistant right now, for the screen.
//
// The screen talks to Home Assistant by entity id, and an entity id is whatever Home Assistant chose
// when it first saw the entity, so the screen cannot work one out from a name. What it can do is
// rebuild the entity's unique id, which is the MAC, the device id, the type and the name, and look that
// up in Home Assistant's entity registry. This file gives it those four, as announced at this moment:
// they change with the grouping stage and with a rename, so it is rewritten every time the device
// announces.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Path is where the manifest lives. The directory holds nothing else, so the screen's chroot can mount
// it whole, read-only, and still see each replacement.
const Path = layout.StateDir + "/export/ha-entities.json"

// version is the schema's. The screen refuses a version it does not know rather than guess.
const version = 1

type manifest struct {
	Version  int      `json:"version"`
	MAC      string   `json:"mac"`
	Node     string   `json:"node"`
	Name     string   `json:"name"`
	Entities []entity `json:"entities"`
}

type entity struct {
	ObjectID string `json:"object_id"`
	Type     string `json:"type"`
	DeviceID uint32 `json:"device_id"`
	Name     string `json:"name"`
}

// Placer is a component.Describer that says where the entries it adds to the entity list are announced.
// Those entries are not in the registry's entities, so the announcement does not carry them, but Home
// Assistant registers them like any other and the screen may want them (the camera).
type Placer interface {
	Placements() []component.Placement
}

// Publisher writes the manifest for one registry to one path.
type Publisher struct {
	Path     string
	Registry *component.Registry
}

// Publish writes the manifest for an announcement to Path, if it changed, with whatever the registry's
// describers add to the entity list after it. Call it every time the device announces: at start, after
// each grouping stage and after a rename.
func Publish(mac, node, name string, a component.Announcement) error {
	return Publisher{Path: Path, Registry: component.Default()}.Publish(mac, node, name, a)
}

// Publish is the package's Publish, for this publisher's registry and path.
func (p Publisher) Publish(mac, node, name string, a component.Announcement) error {
	ents := slices.Clone(a.Entities)
	for _, h := range p.Registry.Describers() {
		if pl, ok := h.(Placer); ok {
			ents = append(ents, pl.Placements()...)
		}
	}
	a.Entities = ents
	data, err := Build(mac, node, name, a)
	if err != nil {
		return err
	}
	_, err = Write(p.Path, data)
	return err
}

// Build is the manifest for a device with this MAC, node name (the slug Home Assistant's actions are
// named after) and display name, announcing a.
//
// The MAC is written the way Home Assistant writes it into unique ids (format_mac: lower case, colon
// separated), whatever form it comes in, since the screen matches it as a string.
func Build(mac, node, name string, a component.Announcement) ([]byte, error) {
	m := manifest{Version: version, MAC: layout.MAC(mac), Node: node, Name: name, Entities: []entity{}}
	if m.MAC == "" {
		return nil, fmt.Errorf("manifest: %q is not a MAC address", mac)
	}
	for _, p := range a.Entities {
		m.Entities = append(m.Entities, entity{ObjectID: p.ObjectID, Type: p.Type, DeviceID: p.DeviceID, Name: p.Name})
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Write puts data at path, creating its directory, unless the file already holds exactly that; it
// reports whether it wrote. The device announces on every connection, and the screen watches the file,
// so an unchanged manifest is left alone.
//
// The file is replaced whole, by rename: the screen may read it at any moment and must see the old one
// or the new one, never one being written. Both are readable by everyone, since the screen reads them as
// another user from inside its chroot; nothing in them is secret.
func Write(path string, data []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	// MkdirAll's mode is cut by the umask, and a directory left from before keeps whatever it had.
	if err := os.Chmod(dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(dir, ".ha-entities-*")
	if err != nil {
		return false, err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}
