package voice

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// The reply face is what the screen shows while the answer is heard. Home Assistant hands over the
// reply's text and url seconds before speech synthesis has produced any of it, so the screen keeps
// the thinking face until the first of the reply's audio is queued, whichever way it arrives.

// faceTurn is a turn that has stopped listening, with what a screen is told recorded. Events the turn
// posts to itself (the reply's errands) are handled on the test's goroutine, as the loop would.
type faceTurn struct {
	t    *testing.T
	c    *conversation
	seen []State
}

func newFaceTurn(t *testing.T, setup ...func()) *faceTurn {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, fn := range setup {
		fn()
	}
	c := newConversation(&esphome.VoiceSatellite{})
	c.claim = c.leds.Claim(led.PriorityTurn)
	f := &faceTurn{t: t, c: c}
	stop := Changed.Listen(func(s State) { f.seen = append(f.seen, s) })
	t.Cleanup(func() {
		c.handle(event{kind: evCancel})
		stop()
		c.claim.Release()
	})
	c.think()
	return f
}

// screen is the phase the screen was last told.
func (f *faceTurn) screen() string {
	if len(f.seen) == 0 {
		return ""
	}
	return f.seen[len(f.seen)-1].Phase
}

// settle handles what the turn posts to itself for a while, as the loop would.
func (f *faceTurn) settle(d time.Duration) {
	end := time.After(d)
	for {
		select {
		case e := <-f.c.events:
			f.c.handle(e)
		case <-end:
			return
		}
	}
}

// until handles posted events until the screen shows phase, or fails the test.
func (f *faceTurn) until(phase string) {
	f.t.Helper()
	end := time.After(5 * time.Second)
	for f.screen() != phase {
		select {
		case e := <-f.c.events:
			f.c.handle(e)
		case <-end:
			f.t.Fatalf("the screen shows %q, never %q", f.screen(), phase)
		}
	}
}

// heldSpeech serves a second of speech as a whole WAVE file, but only once release is closed: until
// then it is Home Assistant still synthesizing it.
func heldSpeech(t *testing.T) (url string, release chan struct{}) {
	t.Helper()
	release = make(chan struct{})
	pcm := make([]byte, speaker.VoiceRate*2)
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint32(speaker.VoiceRate))
	binary.Write(&b, binary.LittleEndian, uint32(speaker.VoiceRate*2))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(b.Bytes())
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	return srv.URL + "/reply.wav", release
}

func TestReplyTextAndURLKeepThinking(t *testing.T) {
	f := newFaceTurn(t)
	url, release := heldSpeech(t)

	f.c.handle(event{kind: evReplyText, text: "It's noon."})
	f.c.handle(event{kind: evReplyURL, url: url})
	f.settle(200 * time.Millisecond)

	if got := f.screen(); got != "thinking" {
		t.Fatalf("with no reply audio yet the screen shows %q, want thinking", got)
	}
	if got := f.seen[len(f.seen)-1].Reply; got != "It's noon." {
		t.Errorf("the reply's text is not available while thinking: %q", got)
	}

	close(release)
	f.until("replying")
}

// The agent's holding phrase is the first thing heard, so its audio is what brings the reply face,
// not the play_ack asking for it.
func TestAckAudioBringsTheReplyFace(t *testing.T) {
	f := newFaceTurn(t)
	url, release := heldSpeech(t)

	f.c.handle(event{kind: evAck, url: url})
	f.settle(200 * time.Millisecond)
	if got := f.screen(); got != "thinking" {
		t.Fatalf("with the ack still being synthesized the screen shows %q, want thinking", got)
	}

	close(release)
	f.until("replying")
}

// A reply pushed over the API brings the reply face with its first chunk, text or no text before it.
func TestPushedAudioBringsTheReplyFace(t *testing.T) {
	f := newFaceTurn(t)

	f.c.handle(event{kind: evReplyText, text: "Bedroom light is off."})
	if got := f.screen(); got != "thinking" {
		t.Fatalf("the reply's text alone moved the screen to %q", got)
	}
	f.c.handle(event{kind: evStreamAudio, audio: make([]byte, speaker.VoiceRate*2)})
	f.until("replying")
}

// Canceling while the reply is still being synthesized ends the turn as it always has, and the reply
// that turns up afterwards brings no reply face.
func TestCancelWhileThinking(t *testing.T) {
	f := newFaceTurn(t)
	url, release := heldSpeech(t)

	f.c.handle(event{kind: evReplyText, text: "It's noon."})
	f.c.handle(event{kind: evReplyURL, url: url})
	f.settle(100 * time.Millisecond)
	f.c.handle(event{kind: evCancel})

	if f.c.Phase() != phaseIdle || f.screen() != "idle" {
		t.Fatalf("after cancel the turn is %v and the screen shows %q", f.c.Phase(), f.screen())
	}
	close(release)
	f.settle(300 * time.Millisecond)
	for _, s := range f.seen {
		if s.Phase == "replying" {
			t.Fatal("the canceled reply brought the reply face")
		}
	}
}

// The assistant phase sensor is how a screen drawn by another program follows the same faces: it
// says what voice.Changed says, so it waits for the reply's audio exactly as the screen does.
func TestAssistantPhaseFollowsTheFace(t *testing.T) {
	p := newPhaseSensor()
	t.Cleanup(p.stop)
	if got := p.entity.Get(); got != "idle" {
		t.Fatalf("before any turn the sensor says %q, want idle", got)
	}
	if p.entity.ObjectID != "assistant_phase" || p.entity.Name != "Assistant phase" {
		t.Errorf("the sensor is %q (%q)", p.entity.ObjectID, p.entity.Name)
	}

	f := newFaceTurn(t)
	url, release := heldSpeech(t)
	f.c.handle(event{kind: evReplyText, text: "It's noon."})
	f.c.handle(event{kind: evReplyURL, url: url})
	f.settle(100 * time.Millisecond)
	if got := p.entity.Get(); got != "thinking" {
		t.Fatalf("with no reply audio yet the sensor says %q, want thinking", got)
	}

	close(release)
	f.until("replying")
	// The phase and nothing else: the Spot never shows what was heard or said.
	seen := map[string]bool{"idle": true, "listening": true, "thinking": true, "replying": true}
	if got := p.entity.Get(); !seen[got] {
		t.Fatalf("the sensor carries %q, which is not a phase", got)
	}
	deadline := time.Now().Add(time.Second)
	for p.entity.Get() != "replying" {
		if time.Now().After(deadline) {
			t.Fatalf("the screen shows replying and the sensor says %q", p.entity.Get())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The ring's reply animation starts with the reply's audio, as the screen's reply face does; until
// then the ring goes on showing the thinking animation.
func TestRingRepliesWithTheAudio(t *testing.T) {
	f := newFaceTurn(t, func() {
		w := config.Set().Wake(0)
		if err := w.ThinkingEffect("test-thinking"); err != nil {
			t.Fatal(err)
		}
		if err := w.ReplyingEffect("test-replying"); err != nil {
			t.Fatal(err)
		}
	})
	if got := f.c.claim.Showing().Effect; got != "test-thinking" {
		t.Fatalf("thinking, the ring shows %q", got)
	}
	url, release := heldSpeech(t)

	f.c.handle(event{kind: evReplyText, text: "It's noon."})
	f.c.handle(event{kind: evReplyURL, url: url})
	f.settle(100 * time.Millisecond)
	if got := f.c.claim.Showing().Effect; got != "test-thinking" {
		t.Fatalf("with no reply audio yet the ring shows %q, want the thinking animation", got)
	}

	close(release)
	f.until("replying")
	if got := f.c.claim.Showing().Effect; got != "test-replying" {
		t.Fatalf("with the reply audible the ring shows %q, want the replying animation", got)
	}
}

// Canceling a whole-file reply while it is still downloading is a deliberate stop, not the url
// failing: the slot keeps fetching its replies whole.
func TestCancelDuringWholeFileKeepsDelivery(t *testing.T) {
	f := newFaceTurn(t)
	url, _ := heldSpeech(t)

	f.c.handle(event{kind: evReplyText, text: "It's noon."})
	f.c.handle(event{kind: evReplyURL, url: url})
	f.settle(100 * time.Millisecond)
	f.c.handle(event{kind: evCancel})
	f.settle(300 * time.Millisecond)

	if got := wakeword.Delivery(0); got != config.DeliveryWhole {
		t.Fatalf("after a cancel the slot's delivery is %q, want %q", got, config.DeliveryWhole)
	}
}
