package eventsources

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	m "mux/internal/multiplexer"
	pt "mux/internal/packet"
	"mux/internal/state"
	"mux/internal/utils"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

var since = func(t time.Time) int64 {
	if t.Equal(time.Time{}) {
		return 0
	}
	return time.Since(t).Milliseconds()
}

type PeerInfo struct {
	cancel context.CancelFunc
	inChan chan m.Event
}

type PacketSender struct {
	m.Named
	log *slog.Logger

	PeerInfoMap map[state.Addr2]PeerInfo
	wg          sync.WaitGroup

	instanceID   atomic.Int32
	advertIntvMs atomic.Int32
}

func NewPacketSender(log *slog.Logger, name string) *PacketSender {
	return &PacketSender{
		Named: m.Named{EvName: name},
		log:   log,

		PeerInfoMap: make(map[state.Addr2]PeerInfo),
		wg:          sync.WaitGroup{},

		instanceID:   atomic.Int32{},
		advertIntvMs: atomic.Int32{},
	}
}

func (p *PacketSender) Start(ctx context.Context, config <-chan state.Config, in <-chan m.Event, out chan<- m.Event) {
	defer func() {
		for _, peerInfo := range p.PeerInfoMap {
			if peerInfo.cancel != nil {
				peerInfo.cancel()
			}
		}
		p.wg.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case cfg := <-config:
			if cfg.Id == 0 && cfg.AdvertIntv == 0 {
				p.log.Error("incomplete cfg", slog.Any("cfg", cfg))
				continue
			}
			p.log.Info("cfg", "cfg", cfg)

			p.instanceID.Store(int32(cfg.Id))
			p.advertIntvMs.Store(int32(cfg.AdvertIntv))

			runningPeers := slices.Collect(maps.Keys(p.PeerInfoMap))

			toRemovePeers := utils.MapSlice(runningPeers, func(runningPeer state.Addr2) (state.Addr2, bool) {
				return runningPeer, !slices.Contains(cfg.PeerAddrs, runningPeer)
			})

			for _, toRemove := range toRemovePeers {
				p.log.Info("removing sender for peer", slog.Any("peer", toRemove))
				p.stopSender(&toRemove)
			}

			toAddPeers := utils.MapSlice(cfg.PeerAddrs, func(cfgPeer state.Addr2) (state.Addr2, bool) {
				return cfgPeer, !slices.Contains(runningPeers, cfgPeer)
			})

			for _, toAdd := range toAddPeers {
				p.log.Info("adding sender for peer", slog.Any("peer", toAdd))
				p.startSender(ctx, cfg.ListenAddr.IP, &toAdd, out)
			}
		}
	}
}

func (p *PacketSender) UpdateFunc() m.UpdateFunc[state.State] {
	return func(data any, state *state.State) error {
		pkt, ok := data.(pt.PacketTx)
		if !ok {
			return fmt.Errorf("invalid cast")
		}

		buf, ok := state.PacketSentMap[pkt.DstIP]
		if !ok || buf == nil {
			buf = utils.NewRingBuffer[pt.PacketTx](16)
			state.PacketSentMap[pkt.DstIP] = buf
		}

		buf.Write(pkt)
		return nil
	}
}

func (p *PacketSender) stopSender(peer *state.Addr2) {
	peerInfo, ok := p.PeerInfoMap[*peer]
	if !ok {
		return
	}

	if peerInfo.cancel != nil {
		peerInfo.cancel()
	}

	delete(p.PeerInfoMap, *peer)
}

func (p *PacketSender) startSender(ctx context.Context, selfIP netip.Addr, peer *state.Addr2, out chan<- m.Event) error {
	ctx2, cancel := context.WithCancel(ctx)
	peerInfo, ok := p.PeerInfoMap[*peer]
	if !ok {
		peerInfo = PeerInfo{inChan: make(chan m.Event)}
	}
	peerInfo.cancel = cancel
	p.PeerInfoMap[*peer] = peerInfo

	src := &net.UDPAddr{IP: selfIP.AsSlice(), Port: peer.SrcPort}
	dest := &net.UDPAddr{IP: peer.IP.AsSlice(), Port: peer.Port}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.run(ctx2, src, dest, out)
	}()

	return nil
}

func (p *PacketSender) run(ctx context.Context, src, dest *net.UDPAddr, out chan<- m.Event) {
	dstIP, _ := netip.ParseAddr(dest.IP.String())

	var packet pt.PacketTx
	prio := uint16(0)
	isMaster := false
	selfNum := uint16(1)
	peerNum := uint16(0)
	peerNumRxTime := time.Time{}

	for {
		log := p.log.With("rIPPort", dest.String())

		log.Info("Listen udp on", slog.String("selfIPPort", src.String()))
		conn, err := net.ListenUDP("udp", src)
		if err != nil {
			log.Error("unable to listen udp", slog.Any("err", err))
			return
		}

		ticker := time.NewTicker(time.Millisecond * time.Duration(p.advertIntvMs.Load()))
		// perSenderNotifyChan := utils.Forwarder(context.Background(), info.perSenderNotifyChan)

	loop:
		for {
			if ctx.Err() != nil {
				conn.Close()
				return
			}

			select {
			case <-ctx.Done():
				conn.Close()
				return

			case <-ticker.C:
				selfNum += 1
				if selfNum == 0 {
					selfNum += 1
				}

				packet = pt.PacketTx{
					Packet: pt.Packet{
						InstanceID: uint8(p.instanceID.Load()),
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

				out <- m.Event{Name: p.EvName, Data: packet}
				log.Debug("send OK", slog.Int("n", n), slog.Any("packet", packet))

				// case n := <-info.globalSenderNotifyChan:
				// 	log.Debug("globalSenderNotifyChan rx", slog.Any("event", n.String()))
				// 	isMaster = n.haState == MasterState || n.haState == MasterRxLowerPriState
				// 	prio = uint16(n.prio)

				// case n := <-perSenderNotifyChan:
				// 	log.Debug("-- perSenderNotifyChan rx", slog.Any("event", n.String()))
				// 	peerNum = n.peerNum
				// 	peerNumRxTime = n.peerNumRxTime
			}
		}
	}
}
