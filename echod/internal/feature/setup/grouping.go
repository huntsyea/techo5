package setup

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/api"
)

// Grouping settings into sub-devices.
//
// Home Assistant shows the device's settings under sub-devices beneath it, one per area (Voice,
// Screen and so on). Turning that off puts them all on the device itself. The entity ids are kept,
// because the device moves them in three steps Home Assistant can follow, one per connection
// (api/grouping.go), so it takes a few reconnections rather than happening at once; and while it is
// under way the page says which step it is on.

// groupingSection is the switch, and how far a change of it has got.
func groupingSection(w http.ResponseWriter, token string) {
	grouped := config.Get().Grouping.Grouped
	fmt.Fprint(w, `<fieldset><legend>Home Assistant devices</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "grouping", "general")
	checked := ""
	if grouped {
		checked = " checked"
	}
	fmt.Fprintf(w, `<p style="margin:0"><label><input type="checkbox" name="grouped" value="yes"%s style="width:auto">
	 Group settings into sub-devices</label></p>
	 <p class="note">Off puts every setting on this one device in Home Assistant. The entity ids stay as
	  they are; the move happens over a few reconnections.</p>`, checked)
	if step, of := groupingStep(grouped, api.Get().Stage()); step > 0 {
		where := "onto one device"
		if grouped {
			where = "back into sub-devices"
		}
		fmt.Fprintf(w, `<p><strong>Moving settings %s: step %d of %d.</strong></p>`, where, step, of)
	}
	fmt.Fprint(w, `<p><button type="submit">Save</button></p></form></fieldset>`)
}

// groupingStep is which step of a move the device is on, counting from the end it started at, and how
// many there are; zero when it is where the setting asks.
func groupingStep(grouped bool, stage config.Stage) (step, of int) {
	next := component.Next(grouped, stage)
	if next == stage {
		return 0, 0
	}
	of = len(config.Stages) - 1
	step = slices.Index(config.Stages, next)
	if grouped {
		step = of - step
	}
	return step, of
}

func saveGrouping(grouped bool) string {
	if err := api.Get().SetGrouped(grouped); err != nil {
		return "could not save it: " + err.Error()
	}
	slog.Info("setup page: grouping settings into sub-devices set", "grouped", grouped)
	return ""
}
