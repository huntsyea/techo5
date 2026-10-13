package api

// Where entities are announced, grouped onto sub-devices or flattened onto the device, and the staged
// way between the two (component/grouping.go says why there are stages at all).
//
// The rule this file keeps is that Home Assistant sees every stage before the device moves past it. A
// step is taken only once Home Assistant has finished a connection at the current stage, which is it
// subscribing to state: by then it has read the device info and the entity list and acted on both. Any
// other client subscribing counts for nothing (homeAssistantSubscribed). The new stage is saved before
// anything else, so a restart from here on comes up at it rather than at the one before; then every
// client is dropped, and the new placements are put on the entities while nothing is connected, which
// is the only time nothing reads them. Home Assistant reconnects within seconds, sees the new stage,
// and that completed connection is what allows the next step.
//
// So a device that restarts, or whose Home Assistant is away, resumes at the stage it saved and waits
// for Home Assistant there. A saved stage it does not know is not fatal: it announces the stage its
// setting ends at (startMigration). One already at the stage its setting asks for never asks for a
// reconnect, which is the device as it has always been.

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// settings is where the grouping setting and stage are kept: the device's store, or a test's.
type settings interface {
	Get() config.Config
	Set() config.Writer
}

// stored is the device's own store.
type stored struct{}

func (stored) Get() config.Config { return config.Get() }
func (stored) Set() config.Writer { return config.Set() }

type migration struct {
	cfg      settings
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

// startMigration announces the stage the device saved in cfg. It fails on placements that could not be
// announced at every stage, two names that would clash among them, which is a device that should not
// start rather than one that fails only when somebody turns grouping off.
func startMigration(cfg settings, name string, ents []esphome.Entity) (*migration, component.Announcement, error) {
	g := cfg.Get().Grouping
	stage := g.Stage
	if !stage.Valid() {
		// A file this build cannot follow is no reason not to start, which would only restart into the
		// same file. The stage at the setting's end is the best guess at what Home Assistant last saw,
		// and the file is left as it is: the next real step saves over it, and until then a look at it
		// shows what was there.
		stage = config.Ends(g.Grouped)
		slog.Error("the saved grouping stage is unknown; announcing the one the setting ends at",
			"saved", g.Stage, "grouped", g.Grouped, "announcing", stage)
	}
	// Declared before anything is applied: Apply writes over what it reads.
	declared, err := component.Declared(ents)
	if err != nil {
		return nil, component.Announcement{}, err
	}
	a, err := component.Announce(name, declared, stage)
	if err != nil {
		return nil, component.Announcement{}, err
	}
	if err := component.Apply(ents, a); err != nil {
		return nil, component.Announcement{}, err
	}
	return &migration{cfg: cfg, name: name, declared: declared, ents: ents, stage: stage}, a, nil
}

// subscribed is Home Assistant having finished a connection at the current stage. It reports whether a
// step was saved, and so whether every client should be dropped to have it announced.
func (r *migration) subscribed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = true
	return r.step()
}

// settingChanged is the setting having changed. A stage Home Assistant has already seen can be left at
// once; otherwise the next completed connection does it.
func (r *migration) settingChanged() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.step()
}

// step saves the next stage toward the setting, if there is one and it is time. A save that fails is no
// step: announcing a stage the device would not come back up at could skip one after a restart, so it
// waits for the next connection and tries again.
func (r *migration) step() bool {
	if !r.seen || r.due != "" {
		return false
	}
	next := component.Next(r.cfg.Get().Grouping.Grouped, r.stage)
	if next == r.stage {
		return false
	}
	if err := r.cfg.Set().Grouping().Stage(next); err != nil {
		slog.Error("saving the next grouping stage failed; staying where home assistant last saw the device",
			"stage", r.stage, "next", next, "err", err)
		return false
	}
	r.due = next
	return true
}

// applyDue puts a saved stage on the entities and hands back the announcement, for its sub-devices. It
// is only for when no client is connected, since a connection reads the entity list and the device
// info without a lock.
func (r *migration) applyDue() (component.Announcement, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.due == "" {
		return component.Announcement{}, false
	}
	// Announce cannot fail here: startMigration already asked it about every stage.
	a, err := component.Announce(r.name, r.declared, r.due)
	if err == nil {
		err = component.Apply(r.ents, a)
	}
	if err != nil {
		slog.Error("announcing the next grouping stage failed", "stage", r.stage, "next", r.due, "err", err)
		return component.Announcement{}, false
	}
	slog.Info("grouping stage changed", "from", r.stage, "to", r.due,
		"grouped", r.cfg.Get().Grouping.Grouped)
	r.stage, r.due, r.seen = r.due, "", false
	return a, true
}

// SetGrouped is the "group settings into sub-devices" setting: it saves it and starts the device
// toward it, one stage per connection Home Assistant completes, taking the first step at once if Home
// Assistant has already seen the stage the device is at. Turning it back on partway walks back the
// same way. Stage says how far the device has got.
func (a *API) SetGrouped(v bool) error {
	if err := a.settings().Set().Grouping().Grouped(v); err != nil {
		return err
	}
	if r := a.currentMigration(); r != nil && r.settingChanged() {
		a.Reconnect()
	}
	return nil
}

// Stage is the grouping stage the device is announcing, which differs from the setting while a
// migration is under way. Before the server has started it is the saved one.
func (a *API) Stage() config.Stage {
	if r := a.currentMigration(); r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.stage
	}
	return a.settings().Get().Grouping.Stage
}

func (a *API) settings() settings {
	if a.cfg != nil {
		return a.cfg
	}
	return stored{}
}

func (a *API) currentMigration() *migration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.migration
}

// homeAssistantSubscribed is a handler that watches for Home Assistant subscribing to state, which is
// it having finished a connection. Only Home Assistant's counts: a stage is for its registries, and
// ESPHome's dashboard or a logs client having seen one says nothing about whether it has. It knows
// itself by the hello it opens with (CLIENT_INFO in its ESPHome integration: "Home Assistant
// 2026.9.4"). The library's OnSubscribed does not say which connection subscribed, which is why this
// is a handler: handlers are given the connection, and see the subscribe right after OnSubscribed.
func (a *API) homeAssistantSubscribed(_ context.Context, c *esphome.Conn, msg proto.Message) error {
	if _, ok := msg.(*api.SubscribeStatesRequest); ok && strings.HasPrefix(c.ClientInfo(), "Home Assistant") {
		a.subscribed()
	}
	return nil
}

// subscribed is Home Assistant having finished a connection. It is called on that connection's read
// loop, which must not block, and a step writes the settings file.
func (a *API) subscribed() {
	safe.Go("grouping-step", func() {
		if r := a.currentMigration(); r != nil && r.subscribed() {
			a.Reconnect()
		}
	})
}
