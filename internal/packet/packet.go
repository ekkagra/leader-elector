package packet

import (
	"fmt"
	"time"
)

// packet bit position data
// 0 - 3       => instanceID
// 4 - 7       => number of IPs whose info available
// 8           => 1 = Master / 0 = Backup
// 9 - 23      => Priority
// 24 - x      => 2 bit per IP / rounded up to multiple of byte (8 bits)
// 16 bits     => MyNumber
// 16 bits     => LastPeerNumber
// 8 bits      => RxTimeAgo
// 32/128 bits => OtherIPs
// 8 bits      => rtt
// 8 bits      => lastRxTimeAgo

// type TimeInfo struct {
// 	rtt       uint8
// 	rxTimeAgo uint8
// }

type Packet struct {
	InstanceID uint8
	IsMaster   bool
	Priority   uint16
	SelfNum    uint16
	PeerNum    uint16
	PeerRxAgo  uint16
	// otherIPCount    uint8
	// otherIPInfo     []byte // where v4 or v6
	// otherIPTimeInfo map[netip.Addr]TimeInfo
}

func indexCheck(raw []byte, idx int) error {
	if len(raw) == idx+1 {
		return nil
	}
	return fmt.Errorf("packet has no data till byte idx %d", idx)
}

func (p *Packet) Unmarshal(raw []byte) error {
	if err := indexCheck(raw, 8); err != nil {
		return err
	}
	p.InstanceID = raw[0]
	p.IsMaster = false
	if raw[1]>>7 == 1 {
		p.IsMaster = true
	}
	p.Priority = (uint16(raw[1])&0x7f)<<8 | uint16(raw[2])
	p.SelfNum = uint16(raw[3])<<8 | uint16(raw[4])
	p.PeerNum = uint16(raw[5])<<8 | uint16(raw[6])
	p.PeerRxAgo = uint16(raw[7])<<8 | uint16(raw[8])
	return nil
}

func (p *Packet) Marshal() []byte {
	out := make([]byte, 9)
	out[0] = p.InstanceID

	isMasterBit := 0
	if p.IsMaster {
		isMasterBit = 1
	}
	out[1] = byte(isMasterBit)<<7 | (byte(p.Priority>>8) & 0x7f)
	out[2] = byte(p.Priority)
	out[3] = byte(p.SelfNum >> 8)
	out[4] = byte(p.SelfNum)
	out[5] = byte(p.PeerNum >> 8)
	out[6] = byte(p.PeerNum)
	out[7] = byte(p.PeerRxAgo >> 8)
	out[8] = byte(p.PeerRxAgo)
	return out
}

type PacketRx struct {
	Packet
	RecvTime time.Time
	Src      string
}

func (p *PacketRx) UnmarshalWithTime(raw []byte) error {
	p.RecvTime = time.Now()
	return p.Unmarshal(raw)
}

type PacketTx struct {
	Packet
	SendTime time.Time
	DstIP    string
}

func (p *PacketTx) MarshalAndSetTime() []byte {
	p.SendTime = time.Now()
	return p.Marshal()
}
