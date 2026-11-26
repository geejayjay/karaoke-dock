package player

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"karaokedock/internal/media"
	"karaokedock/internal/settings"
	"karaokedock/internal/store"
)

func ips(s ...string) []net.IP {
	out := make([]net.IP, len(s))
	for i, v := range s {
		out[i] = net.ParseIP(v)
	}
	return out
}

func TestPickLanIP(t *testing.T) {
	cases := []struct {
		name, routed string
		all          []net.IP
		want         string
	}{
		{"routed LAN wins", "192.168.1.5", ips("192.168.1.5", "172.20.0.1"), "192.168.1.5"},
		{"VPN route skipped", "100.126.48.191", ips("100.126.48.191", "172.20.0.1", "192.168.1.9"), "192.168.1.9"},
		{"no internet, no route", "", ips("172.20.0.1", "10.0.0.4"), "10.0.0.4"},
		{"only VPN", "100.100.1.1", ips("100.100.1.1"), "100.100.1.1"},
		{"nothing", "", nil, "127.0.0.1"},
	}
	for _, c := range cases {
		var r net.IP
		if c.routed != "" {
			r = net.ParseIP(c.routed)
		}
		if got := pickLanIP(r, c.all); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestUDPDiscoveryRespondsToProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg, err := settings.Load(ctx, db, map[string]string{
		settings.MediaRoot: filepath.Join(dir, "media"),
		settings.TempRoot:  filepath.Join(dir, "tmp"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pick an unused UDP port.
	probeSock, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probeSock.LocalAddr().(*net.UDPAddr).Port
	_ = probeSock.Close()

	p := New(db, cfg, nil, media.NewFs(filepath.Join(dir, "media")), port)
	go p.watchDiscovery(ctx)

	client, err := net.DialTimeout("udp4", (&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}).String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var reply discoverReply
	for range 10 {
		_ = client.SetDeadline(time.Now().Add(150 * time.Millisecond))
		_, _ = client.Write([]byte(discoverMagic))
		buf := make([]byte, 512)
		n, rerr := client.Read(buf)
		if rerr == nil && json.Unmarshal(buf[:n], &reply) == nil && reply.App == "karaokedock" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if reply.App != "karaokedock" || reply.Port != port {
		t.Fatalf("unexpected discovery reply: %+v", reply)
	}
}
