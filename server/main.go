package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	pt "mux/internal/packet"
	"mux/internal/utils"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	packetsSent            *utils.RingBuffer[pt.PacketTx]
	packetRecv             *pt.PacketRx
	rtts                   *utils.RingBuffer[time.Duration]
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
	recvChan            chan pt.PacketRx
	sendChan            chan pt.PacketTx
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
			perSenderNotifyChan: make(chan SenderNotifyEvent),
			packetsSent:         utils.NewRingBuffer[pt.PacketTx](16),
			rtts:                utils.NewRingBuffer[time.Duration](512),
		}
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
		recvChan:            make(chan pt.PacketRx, 128),
		sendChan:            make(chan pt.PacketTx, 128),
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

		packet := pt.PacketRx{Src: remote.AddrPort().String()}
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

	since := func(t time.Time) int64 {
		if t.Equal(time.Time{}) {
			return 0
		}
		return time.Since(t).Milliseconds()
	}

	srcIP, _ := utils.AddrPortOrDefaults(p.selfIPPort)
	for dstIP, info := range p.remoteMap {
		src := &net.UDPAddr{IP: srcIP, Port: info.srcPort}

		destIP, _ := utils.AddrPortOrDefaults(dstIP)
		dest := &net.UDPAddr{IP: destIP, Port: info.dstPort}
		dstIP, _ := netip.ParseAddr(destIP.String())

		var packet pt.PacketTx
		prio := uint16(0)
		isMaster := false
		selfNum := uint16(1)
		peerNum := uint16(0)
		peerNumRxTime := time.Time{}

		for {
			log = log.With("rIPPort", dstIP)
			log.Info("Listen udp on", slog.String("selfIP", src.String()), slog.Int("port", info.srcPort))
			conn, err := net.ListenUDP("udp", src)
			if err != nil {
				log.Error("unable to listen udp", slog.Any("err", err))
				return
			}
			defer conn.Close()

			ticker := time.NewTicker(time.Millisecond * time.Duration(p.advertIntv))
			perSenderNotifyChan := utils.Forwarder(context.Background(), info.perSenderNotifyChan)

		loop:
			for {
				select {
				case <-ticker.C:
					selfNum += 1
					if selfNum == 0 {
						selfNum += 1
					}

					packet = pt.PacketTx{
						Packet: pt.Packet{
							InstanceID: 10,
							IsMaster:   isMaster,
							Priority:   prio,
							SelfNum:    selfNum,
							PeerNum:    peerNum,
							PeerRxAgo:  uint16(utils.Min(0xffff, since(peerNumRxTime))),
						},
						DstIP: dstIP,
					}

					data := packet.MarshalAndSetTime()

					n, err := conn.WriteToUDP(data, dest)
					if err != nil {
						log.Error("unable to write to client", slog.Any("err", err))
						conn.Close()
						break loop
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

func (p *PacketHandler) run(ctx context.Context) {
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

	thresholdDuration := time.Millisecond * time.Duration(2*p.advertIntv)
	validFactor := 2
	maxMasterPrioBase := uint16(0xfff) // 15 bits field
	maxMasterPrio := maxMasterPrioBase

	currHAState := BackupState
	prevHAState := BackupState
	currNotifyEvent := SenderNotifyEvent{}
	lastSentNotifyEvent := SenderNotifyEvent{}

	var riseTimeStart *time.Time

	rateMax := 50
	rate := uint16(rand.IntN(rateMax)) + 1

	ticker := time.NewTicker(time.Millisecond * time.Duration(p.evalIntv))
	for {
		select {
		case <-ctx.Done():
			return
		case sent := <-p.sendChan: // Send to IP2:5000
			log.Debug("sendChan", slog.Any("packetTx", sent))

			info, ok := p.remoteMap[sent.DstIP.String()]
			if !ok {
				log.Warn("packet sent not in remoteMap", slog.Any("dstIP", sent.DstIP))
				continue
			}
			info.packetsSent.Write(sent)
			continue
		case recv := <-p.recvChan: // Recv from IP2:xyz
			// update map[netip.Addr]PacketRx
			// PreviousSelfNum = PacketRx.LastPeerNum
			// rtt = PreviousSelfNum.RxTime - PreviousSelfNum.TxTime -(PacketRx.rxTimeAgo)
			log.Debug("recvChan", slog.Any("packetRx", recv))
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

					info.packetRecv = &recv

					sentPackets := info.packetsSent.Values()
					idx := slices.IndexFunc(sentPackets, func(pkt pt.PacketTx) bool {
						return recv.PeerNum == pkt.SelfNum
					})
					if idx != -1 {
						if recv.PeerRxAgo != 0xffff {
							matchingPacket := sentPackets[idx]
							rtt := recv.RecvTime.Sub(matchingPacket.SendTime) - (time.Millisecond * time.Duration(recv.PeerRxAgo))
							log.Info("rtt calculated", "rtt", rtt.Microseconds())
							// info.rtts.Write(rtt)
						}
					} else {
						log.Info("recv packet not found in sent packets")
					}

					p.remoteMap[dstIP] = info
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

		timeNow := time.Now()
		masterExists := false
		haState := currHAState
		switch haState {
		case FaultState:
			// todo: check if all check_scripts have passed;
			// if so promote to backup

		case BackupState:
			// todo: check checks scripts; if failed fall back to FaulState

			// Any master exists
			if slices.ContainsFunc(slices.Collect(maps.Values(p.remoteMap)), func(ri RemoteInfo) bool {
				if ri.packetRecv != nil {
					if timeNow.Sub(ri.packetRecv.RecvTime) > time.Millisecond*time.Duration(validFactor*p.advertIntv) {
						return false
					}
					return ri.packetRecv.IsMaster
				}
				return false
			}) {
				masterExists = true
				break
			}

			// Any Peer has high prio
			peerHasHighPrio := false
			selfPrio := currNotifyEvent.prio
			for _, info := range p.remoteMap {
				if info.packetRecv == nil {
					continue
				}

				if timeNow.Sub(info.packetRecv.RecvTime) > time.Millisecond*time.Duration(validFactor*p.advertIntv) {
					continue
				}

				if selfPrio < info.packetRecv.Priority {
					peerHasHighPrio = true
					break
				}
			}
			if peerHasHighPrio {
				break
			}

			// Did self cross threshold ?
			if riseTimeStart == nil {
				riseTimeStart = utils.PtrTo(time.Now())
			}
			if time.Since(*riseTimeStart) > thresholdDuration {
				currHAState = MasterState
				maxMasterPrio = maxMasterPrioBase + uint16(rand.Int32N(100))
				riseTimeStart = nil
			}
		case MasterState:
			// todo: check checks scripts; if failed fall back to FaulState

			// todo: if any Peer is master, check prio
			// if self > peer: remain master
			// else; drop master
			peerHasHighPrio := false
			selfPrio := currNotifyEvent.prio
			for _, info := range p.remoteMap {
				if info.packetRecv == nil {
					continue
				}

				if !info.packetRecv.IsMaster {
					continue
				}

				if timeNow.Sub(info.packetRecv.RecvTime) > time.Millisecond*time.Duration(validFactor*p.advertIntv) {
					continue
				}

				if selfPrio < info.packetRecv.Priority {
					peerHasHighPrio = true
					break
				}
			}
			if peerHasHighPrio {
				currHAState = BackupState
				break
			}

			// todo: Are we receiving packets from 1 out of min 2 peers.
			// if 0/2+; drop master; network down
			// if 0/1; remain master
			if len(p.remoteMap) > 1 {
				if !slices.ContainsFunc(slices.Collect(maps.Values(p.remoteMap)), func(ri RemoteInfo) bool {
					if ri.packetRecv != nil {
						return timeNow.Sub(ri.packetRecv.RecvTime) < time.Millisecond*time.Duration(validFactor*p.advertIntv)
					}
					return false
				}) {
					currHAState = BackupState
				}
			}

		}

		// ==========================================================================
		// Evaluate broadcast notification (setting prio based on currState,prevState)
		currNotifyEvent = SenderNotifyEvent{haState: currHAState}

		switch currHAState {
		case FaultState:
			currNotifyEvent.prio = 0
		case BackupState:
			if masterExists {
				currNotifyEvent.prio = 0
				break
			}

			switch prevHAState {
			case BackupState:
				currNotifyEvent.prio = (lastSentNotifyEvent.prio + rate) & uint16(0x7fff)
			default:
				currNotifyEvent.prio = 0
				rate = uint16(rand.IntN(rateMax)) + 1
			}
		case MasterState:
			switch prevHAState {
			case MasterState:
				currNotifyEvent.prio = utils.Min(maxMasterPrio, lastSentNotifyEvent.prio+rate)
			default:
				currNotifyEvent.prio = 0
				rate = uint16(rand.IntN(rateMax)) + 1
			}
		}
		prevHAState = currHAState

		if currNotifyEvent != lastSentNotifyEvent {
			if currNotifyEvent.haState != lastSentNotifyEvent.haState {
				log.Warn("HA state change", "old", lastSentNotifyEvent.haState.String(),
					"new", currNotifyEvent.haState.String())
			}
			p.senderBroadcastChan <- currNotifyEvent
			lastSentNotifyEvent = currNotifyEvent
			log.Info("changed", "currNotifyEvent", currNotifyEvent.String())
		}

		ticker.Reset(time.Millisecond * time.Duration(p.evalIntv))
	}
}

func main() {
	var name string
	var selfIPPort string
	var remoteIPPort string
	var evalIntvMs int
	var advertIntvMs int
	var logLevelStr string
	flag.StringVar(&name, "name", "aaa", "name or id")
	flag.StringVar(&selfIPPort, "selfIPPort", ":5000", "IP:PORT or :PORT format")
	flag.IntVar(&evalIntvMs, "evalIntv", 500, "Main loop Evaluation interval in milliseconds")
	flag.IntVar(&advertIntvMs, "advertIntv", 1000, "Advert interval in milliseconds")
	flag.StringVar(&remoteIPPort, "remoteIPPort", "127.0.0.4:5000", "IP:PORT ot :PORT format")
	flag.StringVar(&logLevelStr, "logLevel", "info", "log level")

	// flag.Parse()

	exit := make(chan os.Signal, 1)
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-exit
		cancel()
	}()

	for {
		if ctx.Err() != nil {
			return
		}

		if _, err := os.Stat("./inputs"); err != nil {
			fmt.Println("sleeping ... waiting for file")
			time.Sleep(time.Millisecond * 600)
			continue
		}
		time.Sleep(time.Second)
		cont, err := os.ReadFile("./inputs")
		if err != nil {
			continue
		}

		flag.CommandLine.Parse(strings.Fields(string(cont)))
		fmt.Println("parsed")
		break
	}

	logLevel := slog.LevelInfo
	logLevel.UnmarshalText([]byte(logLevelStr))

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	log.Warn("main started")
	defer log.Warn("main finished")

	// InstanceID
	ph := NewPacketHandler(log, evalIntvMs, advertIntvMs, selfIPPort, remoteIPPort)
	ph.run(ctx)
}
