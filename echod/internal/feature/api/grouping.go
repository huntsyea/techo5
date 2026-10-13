package api

// Where entities are announced, grouped onto sub-devices or flattened onto the device, and the staged
// way between the two (component/grouping.go says why there are stages at all).
//
// The rule this file keeps is that Home Assistant sees every stage before the device moves past it. A
// step is taken only once Home Assistant has finished a connection at the current stage, which is it
// subscribing to state: by then it has read the device info and the entity list and acted on both. The
// new stage is saved before anything else, so a restart from here on comes up at it rather than at the
// one before; then every client is dropped, and the new placements are put on the entities while
// nothing is connected, which is the only time nothing reads them. Home Assistant reconnects within
// seconds, sees the new stage, and that completed connection is what allows the next step.
//
// So a device that restarts, or whose Home Assistant is away, resumes at the stage it saved and waits
// for Home Assistant there. One already at the stage its setting asks for never asks for a reconnect,
// which is the device as it has always been.

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

type regrouping struct {
	name     string
	declared []component.Placement
	ents     []esphome.Entity

	mu sync.Mutex

	// stage is what the entities and the device info say now.
	stage config.Stage

	// seen is whether Home Assistant has finished a connection at stage, which is what a step waits for.
	seen bool

	// due is a stage already saved and not yet announced, or empty. While there is one, nothing steps
	// again: the stage after it has not been seen either.
	due config.Stage
}

// newRegrouping announces the stage the device saved. It fails on placements that could not be
// announced at every stage, two names that would clash among them, which is a device that should not
// start rather than one that fails only when somebody turns grouping off.
func newRegrouping(name string, ents []esphome.Entity, stage config.Stage) (*regrouping, []esphome.Device, error) {
	// Declared before anything is applied: Apply writes over what it reads.
	declared, err := component.Declared(ents)
	if err != nil {
		return nil, nil, err
	}
	a, err := component.Announce(name, declared, stage)
	if err != nil {
		return nil, nil, err
	}
	if err := component.Apply(ents, a); err != nil {
		return nil, nil, err
	}
	return &regrouping{name: name, declared: declared, ents: ents, stage: stage}, a.Devices, nil
}

// subscribed is Home Assistant having finished a connection at the current stage. It reports whether a
// step was saved, and so whether every client should be dropped to have it announced.
func (r *regrouping) subscribed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = true
	return r.step()
}

// changed is the setting having changed. A stage Home Assistant has already seen can be left at once;
// otherwise the next completed connection does it.
func (r *regrouping) changed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.step()
}

// step saves the next stage toward the setting, if there is one and it is time. A save that fails is no
// step: announcing a stage the device would not come back up at could skip one after a restart, so it
// waits for the next connection and tries again.
func (r *regrouping) step() bool {
	if !r.seen || r.due != "" {
		return false
	}
	next := component.Next(config.Get().Grouping.Grouped, r.stage)
	if next == r.stage {
		return false
	}
	if err := config.Set().Grouping().Stage(next); err != nil {
		slog.Error("saving the next grouping stage failed; staying where home assistant last saw the device",
			"stage", r.stage, "next", next, "err", err)
		return false
	}
	r.due = next
	return true
}

// between puts a saved stage on the entities and hands back the sub-devices to announce with it. It
// is only for when no client is connected, since a connection reads the entity list and the device
// info without a lock.
func (r *regrouping) between() ([]esphome.Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.due == "" {
		return nil, false
	}
	// Announce cannot fail here: newRegrouping already asked it about every stage.
	a, err := component.Announce(r.name, r.declared, r.due)
	if err == nil {
		err = component.Apply(r.ents, a)
	}
	if err != nil {
		slog.Error("announcing the next grouping stage failed", "stage", r.stage, "next", r.due, "err", err)
		return nil, false
	}
	slog.Info("grouping stage changed", "from", r.stage, "to", r.due,
		"grouped", config.Get().Grouping.Grouped)
	r.stage, r.due, r.seen = r.due, "", false
	return a.Devices, true
}

// SetGrouped is the "group settings into sub-devices" setting: it saves it and starts the device
// toward it, one stage per connection Home Assistant completes, taking the first step at once if Home
// Assistant has already seen the stage the device is at. Turning it back on partway walks back the
// same way. Stage says how far the device has got.
func (a *API) SetGrouped(v bool) error {
	if err := config.Set().Grouping().Grouped(v); err != nil {
		return err
	}
	if r := a.regrouping(); r != nil && r.changed() {
		a.Reconnect()
	}
	return nil
}

// Stage is the grouping stage the device is announcing, which differs from the setting while a
// migration is under way. Before the server has started it is the saved one.
func (a *API) Stage() config.Stage {
	if r := a.regrouping(); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.stage
	}
	return config.Get().Grouping.Stage
}

func (a *API) regrouping() *regrouping {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.regroup
}

// subscribed is the server's word that Home Assistant has finished a connection. It is called on that
// connection's read loop, which must not block, and a step writes the settings file.
func (a *API) subscribed() {
	safe.Go("regroup", func() {
		if r := a.regrouping(); r != nil && r.subscribed() {
			a.Reconnect()
		}
	})
}
