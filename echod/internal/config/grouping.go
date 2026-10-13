package config

import "fmt"

// Grouping is whether the device's settings sit on sub-devices beneath it in Home Assistant, and how far
// it has got in moving between the two.
//
// Grouped is the setting. Stage is where the device actually is, which is not the same thing while a
// migration is under way: Home Assistant only keeps an entity across a change it can match, so going
// from grouped to flat takes three steps, each announced and seen by Home Assistant before the next
// (component.Announce). Both are saved, so a restart or an absent Home Assistant resumes at the same
// step instead of starting over or skipping one.
//
// On by default, which is how the device has always been.
type Grouping struct {
	Grouped bool  `json:"grouped"`
	Stage   Stage `json:"stage"`
}

// Stage is one step between grouped and flat, in that order. The identifiers are what the file holds.
type Stage string

const (
	// StageGrouped is every entity on its sub-device under the name it was declared with.
	StageGrouped Stage = "grouped"

	// StageRenamed is every entity still on its sub-device, under the name it will have on the device
	// itself. Same slot, new name: Home Assistant reads it as a rename.
	StageRenamed Stage = "renamed"

	// StageMoved is every entity on the device itself under that same name, with the sub-devices still
	// announced and now empty. Same name, new device: Home Assistant reads it as a move.
	StageMoved Stage = "moved"

	// StageFlat is the sub-devices no longer announced, which Home Assistant answers by removing them.
	StageFlat Stage = "flat"
)

// Stages is every stage, from grouped to flat.
var Stages = []Stage{StageGrouped, StageRenamed, StageMoved, StageFlat}

func (s Stage) valid() bool {
	for _, v := range Stages {
		if v == s {
			return true
		}
	}
	return false
}

func defaultGrouping() Grouping { return Grouping{Grouped: true, Stage: StageGrouped} }

type GroupingWriter struct{ st *Store }

func (w GroupingWriter) Grouped(v bool) error {
	return w.st.Update(func(c *Config) { c.Grouping.Grouped = v })
}

func (w GroupingWriter) Stage(s Stage) error {
	if !s.valid() {
		return fmt.Errorf("config: grouping stage %q", s)
	}
	return w.st.Update(func(c *Config) { c.Grouping.Stage = s })
}
