package player

import (
	"net"
	"testing"
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
