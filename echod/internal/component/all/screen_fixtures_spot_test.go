//go:build spot

package all

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/component/manifest"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The screen (spot-home, spot-ui/qml/Resolver.js) finds the Spot's entities in Home Assistant from the
// manifest this build publishes, and its tests run against copies of real manifests rather than ones
// written by hand. These are those manifests: the Spot's real entities, published the way the API
// module publishes them, at each stage of the flatten and after a rename to a name with a space in it.
//
// To regenerate, after changing an entity, a flattened name or the manifest itself, from echod/:
//
//	go test -tags spot ./internal/component/all -run TestScreenFixtures -update
//	cp internal/component/all/testdata/screen/*.manifest.json <spot-home>/spot-ui/tests/fixtures/resolver/
//
// then run spot-ui's tests (node --test spot-ui/tests/resolver_test.js), which check the registry
// fixtures beside them against these.

var update = flag.Bool("update", false, "rewrite testdata/screen from the real entities")

// screenFixtures is each manifest the screen's tests use: the Spot as it is today, the two stages
// between, and flat once renamed to "Bedroom Spot".
var screenFixtures = []struct {
	file  string
	name  string
	stage config.Stage
}{
	{"grouped", "Bedroom", config.StageGrouped},
	{"renamed", "Bedroom", config.StageRenamed},
	{"moved", "Bedroom", config.StageMoved},
	{"flat-spot", "Bedroom Spot", config.StageFlat},
}

// onTheDeviceOnly is what the Spot announces that a test cannot: entities whose features look at the
// device before offering them. The SSH switch is there only where dropbear is installed
// (security.sshAvailable), and the camera only where its device nodes are (camera.Available). Both are
// on the device itself under these names at every stage, and the screen uses the SSH switch.
var onTheDeviceOnly = []component.Placement{
	{ObjectID: "ssh", Type: "switch", Name: "SSH"},
	{ObjectID: "camera", Type: "camera", Name: "Camera"},
}

// TestScreenFixtures checks testdata/screen is what this build publishes, or rewrites it with -update.
func TestScreenFixtures(t *testing.T) {
	declared, err := component.Declared(component.Default().Entities())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range screenFixtures {
		a, err := component.Announce(f.name, declared, f.stage)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join("testdata", "screen", f.file+".manifest.json")
		path := want
		if !*update {
			path = filepath.Join(t.TempDir(), "ha-entities.json")
		}
		p := manifest.Publisher{Path: path, Registry: component.Default()}
		id := manifest.Identity{MAC: simMAC, Node: layout.Slug(f.name), Name: f.name}
		a.Entities = append(a.Entities, onTheDeviceOnly...)
		if err := p.Publish(id, a); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if old, err := os.ReadFile(want); err != nil || !bytes.Equal(got, old) {
			t.Errorf("%s is not what this build publishes; regenerate it with -update (see above)", want)
		}
	}
}
