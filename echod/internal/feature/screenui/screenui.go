// Package screenui is what echod offers a screen drawn by another program (an external UI, with the
// display handed off: hardware/screen.HandedOff): the controls such a screen needs that no other
// component gives Home Assistant, which is the only way it can reach the daemon.
//
//   - Cancel voice turn: ends the turn that is running, including the listening that follows a reply,
//     the way TECHO5's own screen ends one with a second tap. Nothing happens when no turn is running.
package screenui

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

func init() {
	component.Register(component.Device, Get(), component.Order(90))
}

type Feature struct {
	cancel *esphome.Button
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = &Feature{
			cancel: &esphome.Button{
				Base: esphome.Base{ObjectID: "cancel_turn", Name: "Cancel voice turn", Icon: "mdi:microphone-off",
					Category: esphome.CategoryDiagnostic},
				OnPress: func() { voice.Get().Cancel() },
			},
		}
	})
	return shared
}

func (f *Feature) Name() string { return "external screen" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.cancel} }
