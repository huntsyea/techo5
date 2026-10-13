package api

import (
	"bufio"
	"encoding/binary"
	"log/slog"
	"net"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// client is a plaintext ESPHome API client that says what it is in its hello, as Home Assistant and
// every other client do.
type client struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

// serve runs the API's own handlers on a loopback server for a, and returns a way to connect to it.
func serve(t *testing.T, a *API) func(info string) *client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &esphome.Server{Handler: esphome.HandlerFunc(a.homeAssistantSubscribed), Logger: slog.New(slog.DiscardHandler)}
	go func() { _ = srv.Serve(t.Context(), ln) }()
	return func(info string) *client {
		nc, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = nc.Close() })
		c := &client{t: t, nc: nc, r: bufio.NewReader(nc)}
		c.send(&api.HelloRequest{ClientInfo: info, ApiVersionMajor: 1, ApiVersionMinor: 10})
		c.read() // HelloResponse
		return c
	}
}

func (c *client) send(m proto.Message) {
	c.t.Helper()
	info, ok := esphome.Lookup(m)
	if !ok {
		c.t.Fatalf("no id for %T", m)
	}
	data, err := proto.Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	frame := binary.AppendUvarint(binary.AppendUvarint([]byte{0}, uint64(len(data))), uint64(info.ID))
	if _, err := c.nc.Write(append(frame, data...)); err != nil {
		c.t.Fatal(err)
	}
}

// read is the next frame's type.
func (c *client) read() uint64 {
	c.t.Helper()
	_ = c.nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.r.ReadByte(); err != nil {
		c.t.Fatal(err)
	}
	n, err := binary.ReadUvarint(c.r)
	if err != nil {
		c.t.Fatal(err)
	}
	typ, err := binary.ReadUvarint(c.r)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.r.Discard(int(n)); err != nil {
		c.t.Fatal(err)
	}
	return typ
}

// subscribe asks for state, then waits for an answer to a ping sent after it, by which time the
// subscribe has been handled: the connection reads one message at a time.
func (c *client) subscribe() {
	c.t.Helper()
	c.send(&api.SubscribeStatesRequest{})
	c.send(&api.PingRequest{})
	pong, _ := esphome.Lookup(&api.PingResponse{})
	for c.read() != uint64(pong.ID) {
	}
}

// savedStage waits up to a second for the saved stage to become want, since a step is saved off the
// connection's read loop.
func savedStage(d *device, want config.Stage) bool {
	for range 100 {
		if d.st.Get().Grouping.Stage == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Only Home Assistant subscribing moves the migration on: it is the one whose registry each stage is
// for. ESPHome's dashboard, a logs client or a script seeing a stage says nothing about whether Home
// Assistant has, and stepping on their word could take Home Assistant past a stage it never saw.
func TestOnlyHomeAssistantSubscribingTakesAStep(t *testing.T) {
	d := start(t, false, config.StageGrouped)
	connect := serve(t, d.api())

	for _, other := range []string{"aioesphomeapi", "ESPHome Logs", ""} {
		connect(other).subscribe()
	}
	if savedStage(d, config.StageRenamed) {
		t.Fatal("a client that is not Home Assistant subscribing took a step")
	}

	connect("Home Assistant 2026.9.4").subscribe()
	if !savedStage(d, config.StageRenamed) {
		t.Fatal("Home Assistant subscribing took no step")
	}
}
