package setup

import (
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The switch starts on, which is how the device has always been, and says nothing of a move.
func TestGroupingSwitchIsOnByDefault(t *testing.T) {
	f, c := in(t)
	page := get(f, "/setup?tab=general", c).Body.String()
	if !strings.Contains(page, "Group settings into sub-devices") {
		t.Fatal("the General tab has no grouping switch")
	}
	if !strings.Contains(page, `name="grouped" value="yes" checked`) {
		t.Error("the switch is not on by default")
	}
	if strings.Contains(page, "step ") {
		t.Error("the page talks of a step with nothing moving")
	}
}

// Turning it off saves the setting, and the page then shows how far the move has got.
func TestTurningGroupingOffSavesItAndShowsTheStep(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"grouping"}, "tab": {"general"}})
	if p := to.Query().Get("problem"); p != "" {
		t.Fatalf("saving said %q", p)
	}
	if config.Get().Grouping.Grouped {
		t.Fatal("grouping is still on")
	}
	page := get(f, "/setup?tab=general", c).Body.String()
	if strings.Contains(page, `name="grouped" value="yes" checked`) {
		t.Error("the switch still shows on")
	}
	if !strings.Contains(page, "Moving settings onto one device: step 1 of 3") {
		t.Error("the page does not say the move has started")
	}
}

// Partway in either direction, the page names the step the device is on.
func TestTheStepShownFollowsTheStage(t *testing.T) {
	for _, c := range []struct {
		grouped bool
		stage   config.Stage
		want    string
	}{
		{false, config.StageRenamed, "Moving settings onto one device: step 2 of 3"},
		{false, config.StageMoved, "Moving settings onto one device: step 3 of 3"},
		{false, config.StageFlat, ""},
		{true, config.StageFlat, "Moving settings back into sub-devices: step 1 of 3"},
		{true, config.StageRenamed, "Moving settings back into sub-devices: step 3 of 3"},
		{true, config.StageGrouped, ""},
	} {
		f, cookie := in(t)
		if err := config.Set().Grouping().Grouped(c.grouped); err != nil {
			t.Fatal(err)
		}
		if err := config.Set().Grouping().Stage(c.stage); err != nil {
			t.Fatal(err)
		}
		page := get(f, "/setup?tab=general", cookie).Body.String()
		switch {
		case c.want == "" && strings.Contains(page, "step "):
			t.Errorf("grouped %t at %s: the page talks of a step with nothing moving", c.grouped, c.stage)
		case c.want != "" && !strings.Contains(page, c.want):
			t.Errorf("grouped %t at %s: the page does not say %q", c.grouped, c.stage, c.want)
		}
	}
}

// Turning it back on is the same form with the box ticked.
func TestTurningGroupingBackOn(t *testing.T) {
	f, c := in(t)
	if err := config.Set().Grouping().Grouped(false); err != nil {
		t.Fatal(err)
	}
	post(t, f, c, url.Values{"what": {"grouping"}, "tab": {"general"}, "grouped": {"yes"}})
	if !config.Get().Grouping.Grouped {
		t.Error("grouping did not turn back on")
	}
}
