package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// wavHeader is what Home Assistant streams: lengths unknown (0), optional LIST chunk before data.
func wavHeader(rate uint32, channels uint16, withList bool) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(0))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, channels)
	binary.Write(&b, binary.LittleEndian, rate)
	binary.Write(&b, binary.LittleEndian, rate*uint32(channels)*2)
	binary.Write(&b, binary.LittleEndian, channels*2)
	binary.Write(&b, binary.LittleEndian, uint16(16))
	if withList {
		b.WriteString("LIST")
		binary.Write(&b, binary.LittleEndian, uint32(3))
		b.Write([]byte{1, 2, 3, 0}) // odd size is padded
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(0))
	return b.Bytes()
}

type fakeSink struct {
	mu     sync.Mutex
	got    []byte
	closed bool
	delay  time.Duration
}

func (f *fakeSink) wait(ctx context.Context, data []byte) bool {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return false
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, data...)
	return true
}
func (f *fakeSink) done() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

func TestSkipWaveHeaderAcceptsStreamingHeader(t *testing.T) {
	for _, list := range []bool{false, true} {
		r := bytes.NewReader(append(wavHeader(speaker.VoiceRate, 1, list), 7, 8))
		if err := skipWaveHeader(r); err != nil {
			t.Fatalf("list=%v: %v", list, err)
		}
		rest := make([]byte, 2)
		if n, _ := r.Read(rest); n != 2 || rest[0] != 7 {
			t.Fatalf("list=%v: header not fully consumed, next %v", list, rest[:n])
		}
	}
}

func TestSkipWaveHeaderRefusesOtherFormats(t *testing.T) {
	for _, tc := range []struct {
		rate     uint32
		channels uint16
	}{{22050, 1}, {speaker.VoiceRate, 2}} {
		err := skipWaveHeader(bytes.NewReader(wavHeader(tc.rate, tc.channels, false)))
		if !errors.Is(err, errNotSpeech) {
			t.Fatalf("%d Hz %d ch: want errNotSpeech, got %v", tc.rate, tc.channels, err)
		}
	}
	if err := skipWaveHeader(bytes.NewReader([]byte("ID3\x04nonsense....."))); err == nil {
		t.Fatal("non-WAVE body accepted")
	}
}

func TestPumpDeliversWholeSamplesInOrderAndCloses(t *testing.T) {
	body := make([]byte, 3*earlyChunk+3) // odd length: the last byte is half a sample
	for i := range body {
		body[i] = byte(i)
	}
	s := &fakeSink{}
	n, err := pump(context.Background(), bytes.NewReader(body), s)
	if err != nil || n != len(body)-1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !bytes.Equal(s.got, body[:len(body)-1]) || !s.closed {
		t.Fatalf("delivered %d bytes, closed=%v", len(s.got), s.closed)
	}
}

func TestPumpStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeSink{delay: time.Second}
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	n, err := pump(ctx, bytes.NewReader(make([]byte, 4*earlyChunk)), s)
	if !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// The point of the change: audio written before the server finishes is delivered before it finishes.
func TestFetchEarlyPlaysWhileStillStreaming(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-wav")
		w.Write(wavHeader(speaker.VoiceRate, 1, false))
		w.Write(make([]byte, earlyChunk)) // "Let me check the forecast."
		w.(http.Flusher).Flush()
		<-release // the agent is still thinking
		w.Write(make([]byte, earlyChunk))
	}))
	defer srv.Close()

	s := &fakeSink{}
	done := make(chan error, 1)
	go func() { _, err := fetchEarly(context.Background(), srv.URL, s); done <- err }()

	deadline := time.After(2 * time.Second)
	for {
		s.mu.Lock()
		got := len(s.got)
		s.mu.Unlock()
		if got >= earlyChunk {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first chunk not delivered before the stream finished")
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 2*earlyChunk || !s.closed {
		t.Fatalf("got %d bytes, closed=%v", len(s.got), s.closed)
	}
}

func TestStreamWaitBlocksInsteadOfDropping(t *testing.T) {
	s := newStream(0, 0, 0)
	for range chunkQueue {
		if !s.wait(context.Background(), []byte{0, 0}) {
			t.Fatal("queue refused before full")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if s.wait(ctx, []byte{0, 0}) {
		t.Fatal("wait on a full queue returned true without anyone draining it")
	}
}
