package manifest

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

func TestBuildGolden(t *testing.T) {
	a := component.Announcement{Entities: []component.Placement{
		{ObjectID: "wake_threshold_1", Type: "number", DeviceID: 4, Name: "Wake word sensitivity"},
		{ObjectID: "mute", Type: "switch", DeviceID: 0, Name: "Mute"},
	}}
	got, err := Build("F0:81:73:10:53:58", "bedroom", "Bedroom", a)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "version": 1,
  "mac": "f0:81:73:10:53:58",
  "node": "bedroom",
  "name": "Bedroom",
  "entities": [
    {
      "object_id": "wake_threshold_1",
      "type": "number",
      "device_id": 4,
      "name": "Wake word sensitivity"
    },
    {
      "object_id": "mute",
      "type": "switch",
      "device_id": 0,
      "name": "Mute"
    }
  ]
}
`
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestBuildMAC(t *testing.T) {
	for _, in := range []string{"f08173105358", "F0:81:73:10:53:58", "f0-81-73-10-53-58\n"} {
		got, err := Build(in, "bedroom", "Bedroom", component.Announcement{})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if !strings.Contains(string(got), `"mac": "f0:81:73:10:53:58"`) {
			t.Errorf("%q gave %s", in, got)
		}
		if !strings.Contains(string(got), `"entities": []`) {
			t.Errorf("no entities should be an empty list, got %s", got)
		}
	}
	for _, in := range []string{"", "f0817310535", "00:00:00:00:00:00"} {
		if _, err := Build(in, "bedroom", "Bedroom", component.Announcement{}); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestWriteCreatesExportDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "export", "ha-entities.json")
	wrote, err := Write(path, []byte("one\n"))
	if err != nil || !wrote {
		t.Fatalf("wrote %v, err %v", wrote, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "one\n" {
		t.Errorf("file holds %q", got)
	}
	// The screen reads it from another user inside a chroot.
	assertMode(t, filepath.Dir(path), 0o755|fs.ModeDir)
	assertMode(t, path, 0o644)
}

// The screen may read at any moment, so it has to see the old file or the new one, never a file being
// written: the new one replaces the old by rename, which a reader holding the old one open can tell.
func TestWriteReplacesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export", "ha-entities.json")
	if _, err := Write(path, []byte("old\n")); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	wrote, err := Write(path, []byte("new\n"))
	if err != nil || !wrote {
		t.Fatalf("wrote %v, err %v", wrote, err)
	}
	if got, _ := io.ReadAll(old); string(got) != "old\n" {
		t.Errorf("the file open before the write now reads %q: it was rewritten in place", got)
	}
	if got, _ := os.ReadFile(path); string(got) != "new\n" {
		t.Errorf("file holds %q", got)
	}
	assertMode(t, path, 0o644)
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the export directory holds %d entries, want only the manifest", len(entries))
	}
}

func TestWriteSkipsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export", "ha-entities.json")
	if _, err := Write(path, []byte("same\n")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	wrote, err := Write(path, []byte("same\n"))
	if err != nil || wrote {
		t.Fatalf("wrote %v, err %v", wrote, err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged manifest was rewritten")
	}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != want {
		t.Errorf("%s is %v, want %v", path, fi.Mode(), want)
	}
}

// describer stands in for the camera: an entity on the list that is not in the library's registry.
type describer struct{ places []component.Placement }

func (describer) Name() string                                               { return "describer" }
func (describer) Handle(context.Context, *esphome.Conn, proto.Message) error { return nil }
func (describer) DescribesEntities()                                         {}
func (d describer) Placements() []component.Placement                        { return d.places }

func TestPublishIncludesDescribedEntities(t *testing.T) {
	r := component.New()
	r.Add(component.Network, describer{places: []component.Placement{{ObjectID: "camera", Type: "camera", Name: "Camera"}}})
	p := Publisher{Path: filepath.Join(t.TempDir(), "export", "ha-entities.json"), Registry: r}

	a := component.Announcement{Entities: []component.Placement{{ObjectID: "mute", Type: "switch", Name: "Mute"}}}
	if err := p.Publish("f08173105358", "bedroom", "Bedroom", a); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p.Path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Build("f08173105358", "bedroom", "Bedroom", component.Announcement{Entities: []component.Placement{
		{ObjectID: "mute", Type: "switch", Name: "Mute"},
		{ObjectID: "camera", Type: "camera", Name: "Camera"},
	}})
	if string(got) != string(want) {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if len(a.Entities) != 1 {
		t.Error("Publish changed the caller's announcement")
	}
}
