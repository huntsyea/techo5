package voice

import (
	"context"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The agent's holding phrase, played the moment it exists.
//
// An agent that has to look something up says so first ("Let me check the forecast."). Home Assistant
// streams that sentence into the reply, but serves the reply's audio only once the whole answer is
// written, so the phrase arrives glued to the answer seconds later. The conversation agent therefore
// synthesizes the phrase on its own and hands its url to the play_ack action, which opens the turn's
// reply with it right away. The answer is appended to the same reply stream when it arrives, by
// whichever path delivers it, so the order is the order it was said, cancelling the turn stops both,
// and loudness and buffering are the reply's own.

// ackFetchTimeout bounds fetching the phrase: one sentence that Home Assistant has already made.
const ackFetchTimeout = 10 * time.Second

// Actions is the play_ack action. url is a TTS url Home Assistant serves (16-bit mono at the voice
// rate, as for announcements); turn is the caller's conversation id, for the log only.
func (v *Voice) Actions() []*esphome.Action {
	return []*esphome.Action{{
		Name: "play_ack",
		Args: []esphome.Arg{
			{Name: "url", Type: esphome.ArgString},
			{Name: "turn", Type: esphome.ArgString},
		},
		Run: func(c esphome.Call) (any, error) {
			url := strings.TrimSpace(c.String("url"))
			if url == "" {
				return nil, nil
			}
			slog.Info("ack requested", "turn", c.String("turn"))
			v.turn.post(event{kind: evAck, url: url})
			return nil, nil
		},
	}}
}

// playAck opens the reply with the phrase at url. Runs on the conversation loop.
func (c *conversation) playAck(url string) {
	c.speak("")
	s := c.reply.stream
	done := make(chan struct{})
	c.reply.ack = true
	c.reply.ackDone = done
	ctx, cancel := context.WithTimeout(context.Background(), ackFetchTimeout)
	c.ackCancel = cancel
	asked := time.Now()
	safe.Go("ack", func() {
		defer close(done)
		defer c.post(event{kind: evAckPumped})
		samples, err := media.Fetch(ctx, url)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("ack fetch failed; the reply plays without it", "err", err)
			}
			return
		}
		if !feedSamples(ctx, s, samples) {
			return
		}
		slog.Info("ack queued", "samples", len(samples), "ms", time.Since(asked).Milliseconds())
	})
}

// followEarly appends the reply from the run's url once the ack is queued (early.go does the rest).
func (c *conversation) followEarly(url string) {
	s, ackDone := c.reply.stream, c.reply.ackDone
	c.reply.early = true
	c.lateAPI = true
	ctx, cancel := context.WithCancel(context.Background())
	c.earlyCancel = cancel
	slog.Info("early tts streaming offered: the reply follows the ack", "slot", c.slot+1)
	safe.Go("early reply", func() {
		select {
		case <-ackDone:
		case <-ctx.Done():
			return
		}
		n, err := fetchEarly(ctx, url, s)
		switch {
		case err != nil && n == 0 && ctx.Err() == nil:
			c.post(event{kind: evEarlyFailed, text: err.Error()})
		case err != nil && ctx.Err() == nil:
			slog.Warn("early reply cut short", "bytes", n, "err", err)
		default:
			slog.Info("early reply fetched", "bytes", n)
		}
	})
}

// appendWhole appends a whole-file reply after the ack and ends the reply with it.
func (c *conversation) appendWhole(url string) {
	s, ackDone := c.reply.stream, c.reply.ackDone
	ctx, cancel := context.WithCancel(context.Background())
	c.earlyCancel = cancel
	safe.Go("reply after ack", func() {
		select {
		case <-ackDone:
		case <-ctx.Done():
			return
		}
		samples, err := media.Fetch(ctx, url)
		if err != nil {
			slog.Error("fetching the reply after the ack failed", "err", err)
		} else {
			feedSamples(ctx, s, samples)
		}
		s.done()
	})
}

// feedSamples queues mono voice-rate samples into the reply stream in 64 ms chunks.
func feedSamples(ctx context.Context, s *stream, samples []int16) bool {
	const per = earlyChunk / 2
	for off := 0; off < len(samples); off += per {
		end := min(off+per, len(samples))
		b := make([]byte, 2*(end-off))
		for i, v := range samples[off:end] {
			b[2*i] = byte(uint16(v))
			b[2*i+1] = byte(uint16(v) >> 8)
		}
		if !s.wait(ctx, b) {
			return false
		}
	}
	return true
}

// closed reports whether ch is closed (nil counts as closed: there is nothing to wait for).
func closed(ch chan struct{}) bool {
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
