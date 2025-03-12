package state

import (
	pt "mux/internal/packet"
	"net/netip"
)

type State struct {
	FileState  int
	PacketRecv map[netip.AddrPort]pt.PacketRx
}
