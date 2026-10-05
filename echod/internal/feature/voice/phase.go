package voice

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// phaseSensor is the phase a screen is shown (State.Phase: idle, listening, thinking, replying), as
// a text sensor, for a screen drawn by another program that can only follow the device through Home
// Assistant. Home Assistant's own satellite state moves to responding when the reply's text arrives,
// seconds before there is anything to hear; this one waits for the audio, as TECHO5's own screen does.
//
// It carries the phase and nothing else: never what was heard or the reply's words, in its state or
// any attribute. The Spot does not show transcripts on screen, on purpose, so the words in State
// (Heard, Reply) stay inside echod and this sensor must not become a way to read them.
//
// Changed runs on the conversation's loop, and setting a sensor writes to Home Assistant's
// connection, so the loop only records the latest phase and a goroutine of its own publishes it. A
// phase overtaken before it was published is skipped: the sensor says where the turn is, not every
// step it took.
type phaseSensor struct {
	entity *esphome.TextSensor
	latest chan string
	quit   chan struct{}
	cancel func()
	once   sync.Once
}

func newPhaseSensor() *phaseSensor {
	p := &phaseSensor{
		entity: &esphome.TextSensor{Base: esphome.Base{ObjectID: "assistant_phase", Name: "Assistant phase",
			Icon: "mdi:account-voice", Category: esphome.CategoryDiagnostic}},
		latest: make(chan string, 1),
		quit:   make(chan struct{}),
	}
	p.entity.Set(phaseIdle.String())
	p.cancel = Changed.Listen(func(s State) { p.offer(s.Phase) })
	safe.Go("assistant phase", p.run)
	return p
}

// offer replaces whatever phase is still waiting to be published. It never blocks.
func (p *phaseSensor) offer(phase string) {
	for {
		select {
		case p.latest <- phase:
			return
		default:
		}
		select {
		case <-p.latest:
		default:
		}
	}
}

func (p *phaseSensor) run() {
	for {
		select {
		case phase := <-p.latest:
			if phase != p.entity.Get() {
				p.entity.Set(phase)
			}
		case <-p.quit:
			return
		}
	}
}

// stop stops following the phase; calling it again does nothing. Only tests need it: the device's
// sensor lasts as long as echod.
func (p *phaseSensor) stop() {
	p.once.Do(func() {
		p.cancel()
		close(p.quit)
	})
}
