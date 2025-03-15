package state

import (
	pt "mux/internal/packet"
	"mux/internal/utils"
	"net/netip"
)

type State struct {
	FileState     int
	PacketRecvMap map[netip.AddrPort]pt.PacketRx
	PacketSentMap map[netip.Addr]*utils.RingBuffer[pt.PacketTx]
}
