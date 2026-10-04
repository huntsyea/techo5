package voice

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Early reply streaming.
//
// Home Assistant names the reply's audio in the run's first event, and once the conversation agent
// streams its answer it says so with tts_start_streaming. From then on the url serves the reply as it
// is synthesized: a WAVE header with no length, then speech sentence by sentence. Playing it from
// that moment is what makes "Let me check the forecast." sound while the agent is still working,
// instead of after the whole answer exists — which is when the copy pushed over the API and the
// whole-file url both arrive.
//
// The url carries the pipeline's TTS options, which for this device are 16-bit mono at
// speaker.VoiceRate (media.Formats), so the body after the header is exactly what the reply stream
// already plays. Anything else is refused and the turn falls back to the late copies.

// earlyChunk is how much is read at a time: 64 ms of speech.
const earlyChunk = speaker.VoiceRate * 2 * 64 / 1000

// earlyConnect bounds reaching Home Assistant; the body itself has no deadline because it lasts as
// long as the agent takes to answer.
const earlyConnect = 5 * time.Second

// earlyMost bounds one reply, as mostAudio does an announcement: five minutes of mono speech.
const earlyMost = 5 * 60 * speaker.VoiceRate * 2

var errNotSpeech = errors.New("early reply is not 16-bit mono speech at the voice rate")

// earlyClient has no overall timeout: cancellation is the context's.
var earlyClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: earlyConnect}).DialContext,
	ResponseHeaderTimeout: 30 * time.Second,
}}

// sink takes the reply's PCM in order. wait blocks until the chunk is queued, so a reply synthesized
// faster than it plays is held back rather than dropped.
type sink interface {
	wait(ctx context.Context, data []byte) bool
	done()
}

// fetchEarly plays url into s as it arrives. It returns how many bytes of speech it delivered; an
// error with zero bytes means nothing was played and the late copies can take over. It always closes
// s when at least one byte was delivered.
func fetchEarly(ctx context.Context, url string, s sink) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := earlyClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s: %s", url, resp.Status)
	}
	if err := skipWaveHeader(resp.Body); err != nil {
		return 0, err
	}
	return pump(ctx, resp.Body, s)
}

// pump copies whole samples from r to s until the body ends.
func pump(ctx context.Context, r io.Reader, s sink) (int, error) {
	sent := 0
	buf := make([]byte, earlyChunk)
	carry := []byte(nil)
	finish := func(err error) (int, error) {
		if sent > 0 {
			s.done()
		}
		return sent, err
	}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := append(carry, buf[:n]...)
			whole := len(data) &^ 1
			carry = append([]byte(nil), data[whole:]...)
			if whole > 0 {
				if sent+whole > earlyMost {
					return finish(fmt.Errorf("more than %d bytes of speech in one reply", earlyMost))
				}
				chunk := append([]byte(nil), data[:whole]...)
				if !s.wait(ctx, chunk) {
					return finish(ctx.Err())
				}
				sent += whole
			}
		}
		if errors.Is(err, io.EOF) {
			return finish(nil)
		}
		if err != nil {
			return finish(err)
		}
	}
}

// skipWaveHeader reads a RIFF/WAVE header up to the start of the data chunk and checks the format is
// what the reply stream plays. A streaming header carries no usable lengths, so they are ignored.
func skipWaveHeader(r io.Reader) error {
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return fmt.Errorf("early reply header: %w", err)
	}
	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return fmt.Errorf("early reply is not WAVE: starts %q", string(head[0:4]))
	}
	sawFormat := false
	for range 16 {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return fmt.Errorf("early reply chunk: %w", err)
		}
		id := string(ch[0:4])
		size := binary.LittleEndian.Uint32(ch[4:8])
		switch id {
		case "data":
			if !sawFormat {
				return errors.New("early reply has data before fmt")
			}
			return nil
		case "fmt ":
			if size < 16 || size > 64 {
				return fmt.Errorf("early reply fmt chunk of %d bytes", size)
			}
			f := make([]byte, size)
			if _, err := io.ReadFull(r, f); err != nil {
				return fmt.Errorf("early reply fmt: %w", err)
			}
			codec := binary.LittleEndian.Uint16(f[0:2])
			channels := binary.LittleEndian.Uint16(f[2:4])
			rate := binary.LittleEndian.Uint32(f[4:8])
			bits := binary.LittleEndian.Uint16(f[14:16])
			if codec != 1 || channels != 1 || rate != speaker.VoiceRate || bits != 16 {
				return fmt.Errorf("%w (codec %d, %d ch, %d Hz, %d bit)", errNotSpeech, codec, channels, rate, bits)
			}
			sawFormat = true
		default:
			if size > 1<<16 {
				return fmt.Errorf("early reply %q chunk of %d bytes", id, size)
			}
			if _, err := io.CopyN(io.Discard, r, int64(size+size&1)); err != nil {
				return fmt.Errorf("early reply %q chunk: %w", id, err)
			}
		}
	}
	return errors.New("early reply: no data chunk in the first 16")
}

// wait queues a chunk for the errand, blocking while the queue is full.
func (s *stream) wait(ctx context.Context, data []byte) bool {
	select {
	case s.chunks <- data:
		return true
	case <-ctx.Done():
		return false
	}
}
