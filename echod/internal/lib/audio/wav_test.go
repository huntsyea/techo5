package audio

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWAVIsAHeaderAndThePCM(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	b := WAV(pcm, 16000, 1)
	if len(b) != 44+len(pcm) || string(b[0:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("not a canonical 44-byte header: % x", b[:min(len(b), 44)])
	}
	le := binary.LittleEndian
	if le.Uint32(b[4:]) != 36+4 || le.Uint16(b[22:]) != 1 || le.Uint32(b[24:]) != 16000 ||
		le.Uint32(b[28:]) != 32000 || le.Uint16(b[32:]) != 2 || le.Uint16(b[34:]) != 16 || le.Uint32(b[40:]) != 4 {
		t.Fatalf("header fields: % x", b[:44])
	}
	if !bytes.Equal(b[44:], pcm) {
		t.Fatalf("pcm: % x", b[44:])
	}

	path := filepath.Join(t.TempDir(), "x.wav")
	if err := WriteWAV(path, pcm, 16000, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, b) {
		t.Fatal("WriteWAV writes something else than WAV returns")
	}
}
