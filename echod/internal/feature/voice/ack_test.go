package voice

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

func TestFeedSamplesLittleEndianInChunks(t *testing.T) {
	s := newStream(0, 0, 0)
	samples := make([]int16, earlyChunk) // two chunks' worth of samples (earlyChunk is bytes)
	samples[0], samples[1] = 1, -2
	if !feedSamples(context.Background(), s, samples) {
		t.Fatal("feed refused")
	}
	first := <-s.chunks
	if len(first) != earlyChunk || !bytes.Equal(first[:4], []byte{1, 0, 0xfe, 0xff}) {
		t.Fatalf("first chunk %d bytes, starts %v", len(first), first[:4])
	}
	if second := <-s.chunks; len(second) != earlyChunk {
		t.Fatalf("second chunk %d bytes", len(second))
	}
}

func TestClosed(t *testing.T) {
	if !closed(nil) {
		t.Error("nil channel should count as closed")
	}
	ch := make(chan struct{})
	if closed(ch) {
		t.Error("open channel reported closed")
	}
	close(ch)
	if !closed(ch) {
		t.Error("closed channel reported open")
	}
}

// The ack is heard first and the reply follows it in the same stream, even when the reply's server
// is ready before the ack has been queued.
func TestReplyFollowsAckInOneStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(wavHeader(speaker.VoiceRate, 1, false))
		w.Write(bytes.Repeat([]byte{9, 9}, earlyChunk/2)) // the reply: samples of 0x0909
	}))
	defer srv.Close()

	s := newStream(0, 0, 0)
	ackDone := make(chan struct{})
	replied := make(chan error, 1)
	go func() { // what followEarly does
		<-ackDone
		_, err := fetchEarly(context.Background(), srv.URL, s)
		replied <- err
	}()
	time.Sleep(20 * time.Millisecond) // the reply is ready first
	ack := make([]int16, earlyChunk/2)
	for i := range ack {
		ack[i] = 0x0101
	}
	feedSamples(context.Background(), s, ack)
	close(ackDone)
	if err := <-replied; err != nil {
		t.Fatal(err)
	}

	var got []byte
	for c := range s.chunks { // fetchEarly closed the stream at the end
		got = append(got, c...)
	}
	if len(got) != 2*earlyChunk || got[0] != 1 || got[earlyChunk] != 9 {
		t.Fatalf("order wrong: %d bytes, first %d, after ack %d", len(got), got[0], got[earlyChunk])
	}
}
