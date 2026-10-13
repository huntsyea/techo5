package config

import (
	"path/filepath"
	"testing"
)

// A device nobody has set groups its settings into sub-devices, as it always has, with no migration
// under way.
func TestGroupedByDefault(t *testing.T) {
	g := load(t).Get().Grouping
	if !g.Grouped || g.Stage != StageGrouped {
		t.Errorf("grouping = %+v, want grouped at the grouped stage", g)
	}
}

// The setting and how far the migration has got both outlive a restart, which is what lets a migration
// interrupted halfway resume where it stopped.
func TestGroupingSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set().Grouping().Grouped(false); err != nil {
		t.Fatal(err)
	}
	if err := st.Set().Grouping().Stage(StageMoved); err != nil {
		t.Fatal(err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if g := again.Get().Grouping; g.Grouped || g.Stage != StageMoved {
		t.Errorf("after a restart grouping = %+v, want ungrouped at the moved stage", g)
	}
}

// A stage this build does not know is refused rather than written, since the file is what the next
// start announces from.
func TestUnknownStageIsRefused(t *testing.T) {
	st := load(t)
	if err := st.Set().Grouping().Stage("sideways"); err == nil {
		t.Error("an unknown stage was saved")
	}
	if g := st.Get().Grouping; g.Stage != StageGrouped {
		t.Errorf("stage = %q after a refused write", g.Stage)
	}
}
