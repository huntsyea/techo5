package voice

import (
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// What Home Assistant is announcing here, for a screen to show.
//
// Every announcement Home Assistant makes on this device arrives the same way, whatever started it:
// the announce action, a question it asks, a conversation it starts, an agent's late answer. So this
// is where the words are known for certain, and a screen that reads them here shows each one once,
// for as long as it is heard, and none that was never played.

// Announcing is an announcement being played.
type Announcing struct {
	Text  string
	Since time.Time
}

type onAir struct {
	mu   sync.Mutex
	now  Announcing
	on   bool
	seq  uint64
	hook hook.Hook[struct{}]
}

var air onAir

// begin shows text and returns what end needs to take that one down. Words-less announcements (a
// chime, a sound) have nothing to show.
func (a *onAir) begin(text string) uint64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	a.mu.Lock()
	a.seq++
	id := a.seq
	a.now, a.on = Announcing{Text: text, Since: time.Now()}, true
	a.mu.Unlock()
	a.hook.Emit(struct{}{})
	return id
}

// end takes announcement id down, unless a later one has replaced it.
func (a *onAir) end(id uint64) {
	a.mu.Lock()
	if id == 0 || id != a.seq || !a.on {
		a.mu.Unlock()
		return
	}
	a.now, a.on = Announcing{}, false
	a.mu.Unlock()
	a.hook.Emit(struct{}{})
}

func (a *onAir) showing() (Announcing, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.now, a.on
}

// Announcing is the Home Assistant announcement being played now, if any. It is set when the
// announcement arrives and cleared when it has been played or stopped.
func (v *Voice) Announcing() (Announcing, bool) { return air.showing() }

// AnnouncingChanged fires when an announcement starts or stops. Listeners must not block.
func (v *Voice) AnnouncingChanged() *hook.Hook[struct{}] { return &air.hook }
