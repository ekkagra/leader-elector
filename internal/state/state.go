package state

import (
	pt "mux/internal/packet"
	"mux/internal/utils"
	"net/netip"
)

type HAState int8

const (
	UnknownState HAState = iota
	FaultState
	BackupState
	MasterState
	MasterRxLowerPriState
)

func (h *HAState) String() string {
	switch *h {
	case UnknownState:
		return "Unknown"
	case FaultState:
		return "Fault"
	case BackupState:
		return "Backup"
	case MasterState:
		return "Master"
	case MasterRxLowerPriState:
		return "MasterRxLowerPri"
	default:
		return "Unknown"
	}
}

type State struct {
	Config        Config
	HAState       HAState
	PrevHAState   HAState
	PacketRecvMap map[netip.Addr]pt.PacketRx
	PacketSentMap map[netip.Addr]*utils.RingBuffer[pt.PacketTx]
}
