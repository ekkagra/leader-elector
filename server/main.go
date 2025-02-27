package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"mux/internal/utils"
	"net"
	"os"
	"sync"
	"time"
)

// Config:
//   instanceID
//   Candidate or Mediator
//   SelfIP
//   RemoteIP1, ...
//   advert interval
//   VIPS to apply
//   Checks:
//     checkPlugins: Result()
//     Interfaces to watch
//     Files to watch

// Main:
//   ConfigReloader:
//   StateMachine:
//   PacketHandler:
//   Checks:
//     Plugins
//     InterfaceWatcher
//     FileWatcher

// ConfigReloader:
// signal watcher which triggers reload
//

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

// ==================================================
// Packet
// ==================================================
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
	p.Priority = (uint16(raw[1])&uint16(127))<<8 | uint16(raw[2])
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
	Dst      string
}

func (p *PacketTx) MarshalAndSetTime() []byte {
	p.SendTime = time.Now()
	return p.Marshal()
}

// ==================================================
// State, Event, Remote
// ==================================================

type HAState int8

const (
	UnknownState HAState = iota
	FaultState
	BackupState
	MasterState
	MasterRxLowerPriState
)

type SenderNotifyEvent struct {
	haState       HAState
	prio          uint16
	peerNum       uint16
	peerNumRxTime time.Time
}

func (s *SenderNotifyEvent) String() string {
	return fmt.Sprintf("SenderNotifyEvent<haState=%d prio=%d peerNum=%d peerNumRxTime=%v>",
		s.haState, s.prio, s.peerNum, s.peerNumRxTime)
}

type RemoteInfo struct {
	dstPort int
	srcPort int
	// sentPacketChan chan PacketTx
	globalSenderNotifyChan <-chan SenderNotifyEvent
	perSenderNotifyChan    chan SenderNotifyEvent
}

// ==================================================
// PacketHandler
// ==================================================

type PacketHandler struct {
	log                 *slog.Logger
	evalIntv            int
	advertIntv          int
	selfIPPort          string
	remoteMap           map[string]RemoteInfo
	wg                  sync.WaitGroup
	recvChan            chan PacketRx
	sendChan            chan PacketTx
	senderBroadcastChan chan SenderNotifyEvent
	fanout              *utils.FanOut[SenderNotifyEvent]
}

func NewPacketHandler(log *slog.Logger, evalIntv, advertInvt int, selfIPPort string, remoteIPPorts ...string) *PacketHandler {
	remoteMap := make(map[string]RemoteInfo, len(remoteIPPorts))
	port := 5001
	for _, rIP := range remoteIPPorts {
		dstIP, dstPort := utils.AddrPortOrDefaults(rIP)
		remoteMap[dstIP.String()] = RemoteInfo{
			dstPort:             int(dstPort),
			srcPort:             port,
			perSenderNotifyChan: make(chan SenderNotifyEvent)}
		port += 1
	}

	senderBroadcastChan := make(chan SenderNotifyEvent)
	return &PacketHandler{
		log:                 log,
		evalIntv:            evalIntv,
		advertIntv:          advertInvt,
		selfIPPort:          selfIPPort,
		remoteMap:           remoteMap,
		wg:                  sync.WaitGroup{},
		recvChan:            make(chan PacketRx, 128),
		sendChan:            make(chan PacketTx, 128),
		senderBroadcastChan: senderBroadcastChan,
		fanout:              utils.NewFanOut(context.Background(), senderBroadcastChan),
	}
}

func (p *PacketHandler) Listen() {
	log := p.log.With("role", "rx")
	defer p.wg.Done()

	log.Info("listening udp")
	defer log.Info("closing udp conn")

	srcIP, srcPort := utils.AddrPortOrDefaults(p.selfIPPort)
	src := &net.UDPAddr{IP: srcIP, Port: int(srcPort)}

	ln, err := net.ListenUDP("udp", src)
	if err != nil {
		log.Error("unable to listen udp", slog.Any("err", err))
		return
	}
	defer ln.Close()

	for {
		var data [1024]byte
		n, remote, err := ln.ReadFromUDP(data[0:])
		if err != nil {
			log.Error("error at read", slog.Any("err", err))
			break
		}
		log.Debug("rx data", slog.Any("remote", remote.String()), slog.Any("data", string(data[:n])))

		packet := PacketRx{Src: remote.AddrPort().String()}
		if err := packet.UnmarshalWithTime(data[:n]); err != nil {
			log.Error("unable to unmarshal packet", slog.Any("err", err))
			continue
		}

		log.Debug("got packet", slog.Any("packet", packet))
		p.recvChan <- packet
	}
}

func (p *PacketHandler) Send() {
	log := p.log.With("role", "tx")
	defer p.wg.Done()

	log.Info("sending udp")
	defer log.Info("closing sending udp conn")

	srcIP, _ := utils.AddrPortOrDefaults(p.selfIPPort)
	for dstIP, info := range p.remoteMap {
		src := &net.UDPAddr{IP: srcIP, Port: info.srcPort}
		destIP, _ := utils.AddrPortOrDefaults(dstIP)
		dest := &net.UDPAddr{IP: destIP, Port: info.dstPort}

		for {
			log = log.With("rIPPort", dstIP)
			log.Info("Listen udp on", slog.String("selfIP", src.String()), slog.Int("port", info.srcPort))
			conn, err := net.ListenUDP("udp", src)
			if err != nil {
				log.Error("unable to listen udp", slog.Any("err", err))
				return
			}
			defer conn.Close()

			var packet PacketTx
			prio := uint16(0)
			isMaster := false
			selfNum := uint16(1)
			peerNum := uint16(0)
			peerNumRxTime := time.Time{}

			since := func(t time.Time) int64 {
				if t.Equal(time.Time{}) {
					return 0
				}
				return time.Since(t).Milliseconds()
			}

			ticker := time.NewTicker(time.Millisecond * time.Duration(p.advertIntv))
			perSenderNotifyChan := utils.Forwarder(context.Background(), info.perSenderNotifyChan)
			for {
				select {
				case <-ticker.C:
					selfNum += 1
					if selfNum == 0 {
						selfNum += 1
					}

					packet = PacketTx{
						Packet: Packet{
							InstanceID: 10,
							IsMaster:   isMaster,
							Priority:   prio,
							SelfNum:    selfNum,
							PeerNum:    peerNum,
							PeerRxAgo:  utils.Min(0xffff, uint16(since(peerNumRxTime))),
						},
						Dst: dest.AddrPort().String(),
					}

					data := packet.MarshalAndSetTime()
					n, err := conn.WriteToUDP(data, dest)
					if err != nil {
						log.Error("unable to write to client", slog.Any("err", err))
						conn.Close()
						break
					}

					p.sendChan <- packet
					log.Debug("send OK", slog.Int("n", n), slog.Any("packet", packet))

				case n := <-info.globalSenderNotifyChan:
					log.Debug("globalSenderNotifyChan rx", slog.Any("event", n.String()))
					isMaster = n.haState == MasterState || n.haState == MasterRxLowerPriState
					prio = uint16(n.prio)

				case n := <-perSenderNotifyChan:
					log.Debug("-- perSenderNotifyChan rx", slog.Any("event", n.String()))
					peerNum = n.peerNum
					peerNumRxTime = n.peerNumRxTime
				}
			}
		}
	}
}

func (p *PacketHandler) run() {
	log := p.log.With("role", "main")

	p.fanout.Run()

	for dstIP, info := range p.remoteMap {
		info.globalSenderNotifyChan = p.fanout.Add(dstIP)
		p.remoteMap[dstIP] = info
	}

	p.wg.Add(1)
	go p.Listen()

	p.wg.Add(1)
	go p.Send()

	// thresholdDuration := 3 * float32(p.advertIntv)

	currState := BackupState
	prevState := BackupState
	currNotifyEvent := SenderNotifyEvent{}
	lastSentNotifyEvent := SenderNotifyEvent{}

	rateMax := 50
	rate := uint16(rand.IntN(rateMax)) + 1

	ticker := time.NewTicker(time.Millisecond * time.Duration(p.evalIntv))
	for {
		select {
		case sent := <-p.sendChan: // Send to IP2:5000
			// update map[netip.Addr]PacketTx
			// Save few SelfNum which were sent to Dst
			log.Info("sendChan", slog.Any("packetTx", sent))

			// p.remoteMap[sent.Dst]

			continue
		case recv := <-p.recvChan: // Recv from IP2:xyz
			// update map[netip.Addr]PacketRx
			// PreviousSelfNum = PacketRx.LastPeerNum
			// rtt = PreviousSelfNum.RxTime - PreviousSelfNum.TxTime -(PacketRx.rxTimeAgo)
			log.Info("recvChan", slog.Any("packetRx", recv))
			// send Sender for this IP:
			//    PacketRx.SelfNum which becomes PacketTx.LastPeerNum
			//    PacketRx.RecvTime so that it calculates PacketTx.PeerRxAgo
			found := false
			recvIP, _ := utils.AddrPortOrDefaults(recv.Src)
			for dstIP, info := range p.remoteMap {
				ip, _ := utils.AddrPortOrDefaults(dstIP)
				if recvIP.Equal(ip) {
					found = true
					log.Debug("sending notification to specific sender", slog.String("sender", recv.Src),
						slog.Any("peerNum", recv.PeerNum), slog.Any("peerRxTime", recv.RecvTime))
					info.perSenderNotifyChan <- SenderNotifyEvent{peerNum: recv.SelfNum, peerNumRxTime: recv.RecvTime}
					break
				}
			}
			if !found {
				log.Warn("recv addr not found in remoteMap", slog.String("recv Src", recv.Src))
				continue
			}
			// case checkScripts, fileChecks, netlinkChecks:
		case <-ticker.C:
		}
		ticker.Stop()

		// Evaluate broadcast notification
		currNotifyEvent = SenderNotifyEvent{haState: currState}
		if currState == FaultState && currState != prevState {
			currNotifyEvent.prio = 0
		} else if currState == FaultState && currState == prevState {
			currNotifyEvent.prio = 0
		} else if currState == BackupState && currState != prevState {
			currNotifyEvent.prio = 0
			rate = uint16(rand.IntN(rateMax)) + 1
		} else if currState == BackupState && currState == prevState {
			currNotifyEvent.prio = (lastSentNotifyEvent.prio + rate) & uint16(0x7fff)
		} else if currState == MasterState && currState != prevState {
			currNotifyEvent.prio = 0
			rate = uint16(rand.IntN(rateMax)) + 1
		} else if currState == MasterState && currState == prevState {
			currNotifyEvent.prio = utils.Min(0x7fff, lastSentNotifyEvent.prio+rate)
		}
		prevState = currState

		if currNotifyEvent != lastSentNotifyEvent {
			p.senderBroadcastChan <- currNotifyEvent
			lastSentNotifyEvent = currNotifyEvent
		}

		// Collect from all Sends about their packet sent:
		//   update map[netip.Addr]PacketTx
		//
		// Broadcast to all Sends about {state,prio}
		//
		// If any -> fault:
		//   broadcast prio=0, state=0
		// if fault:
		//   broadcast prio=0, state=0
		// If any -> backup:
		//   broadcast prio=0 state=0
		// if backup:
		//   broadcast prio=prio+1 state=0
		// if backup -> master:
		//   broadcast prio=0 state=1
		// If master:
		//   broadcast prio=prio+1 state=1

		ticker.Reset(time.Millisecond * time.Duration(p.evalIntv))
	}
}

func main() {
	var name string
	var selfIPPort string
	var remoteIPPort string
	var evalIntvMs int
	var advertIntvMs int
	flag.StringVar(&name, "name", "aaa", "name or id")
	flag.StringVar(&selfIPPort, "selfIPPort", ":5000", "IP:PORT or :PORT format")
	flag.IntVar(&evalIntvMs, "evalIntv", 500, "Main loop Evaluation interval in milliseconds")
	flag.IntVar(&advertIntvMs, "advertIntv", 1000, "Advert interval in milliseconds")
	flag.StringVar(&remoteIPPort, "remoteIPPort", "127.0.0.4:5000", "IP:PORT ot :PORT format")

	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("main started")
	defer log.Info("main finished")

	// InstanceID
	ph := NewPacketHandler(log, evalIntvMs, advertIntvMs, selfIPPort, remoteIPPort)
	ph.run()
}
