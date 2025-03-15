package state

import (
	"fmt"
	"net/netip"
	"time"
)

type SenderNotifyEventType int8

const (
	GlobalNotifyEvent SenderNotifyEventType = iota

	PerSenderNotifyEvent
)

type SenderNotifyEvent struct {
	EventType     SenderNotifyEventType
	DstIP         netip.Addr
	HAState       HAState
	Prio          uint16
	PeerNum       uint16
	PeerNumRxTime time.Time
}

func (s *SenderNotifyEvent) String() string {
	return fmt.Sprintf("SenderNotifyEvent<haState=%d prio=%d peerNum=%d peerNumRxTime=%v>",
		s.HAState, s.Prio, s.PeerNum, s.PeerNumRxTime)
}
