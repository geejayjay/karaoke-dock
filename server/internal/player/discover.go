package player

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"karaokedock/internal/settings"
)

const discoverMagic = "KARAOKEDOCK?"

type discoverReply struct {
	App          string `json:"app"`
	Name         string `json:"name"`
	Port         int    `json:"port"`
	RoomRequired bool   `json:"roomRequired"`
}

// HostLabel is a friendly machine name shown when apps scan the local network.
func HostLabel() string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "KaraokeDock"
	}
	return h
}

// watchDiscovery answers UDP LAN broadcast probes from the Mobile and TV apps
// so they can find the server automatically without typing an IP address.
func (p *Player) watchDiscovery(ctx context.Context) {
	if p.port <= 0 {
		return
	}
	pc, err := net.ListenPacket("udp4", ":"+strconv.Itoa(p.port))
	if err != nil {
		slog.Debug("udp discovery unavailable", "port", p.port, "err", err)
		return
	}
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	buf := make([]byte, 256)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if strings.TrimSpace(string(buf[:n])) != discoverMagic {
			continue
		}
		raw, err := json.Marshal(discoverReply{
			App:          "karaokedock",
			Name:         HostLabel(),
			Port:         p.port,
			RoomRequired: p.cfg.Get(settings.RoomCode) != "",
		})
		if err == nil {
			_, _ = pc.WriteTo(raw, addr)
		}
	}
}
