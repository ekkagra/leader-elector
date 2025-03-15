package reconciler

import (
	"context"
	"log/slog"
	"maps"
	"math/rand/v2"
	m "mux/internal/multiplexer"
	"mux/internal/packet"
	s "mux/internal/state"
	"mux/internal/utils"
	"slices"
	"time"
)

const (
	thresholdFactor   = 3
	rateMax           = 50
	validFactor       = 3
	maxMasterPrioBase = uint16(0xfff)
)

type Reconciler struct {
	log        *slog.Logger
	evalIntv   time.Duration
	advertIntv time.Duration

	maxMasterPrio uint16
	riseTimeStart *time.Time
	rate          uint16

	lastGlobalNotifyEvent s.SenderNotifyEvent
}

func NewReconciler(log *slog.Logger) *Reconciler {
	rateMax := 50

	return &Reconciler{
		log:        log,
		evalIntv:   time.Millisecond * 100,
		advertIntv: time.Millisecond * 500,

		maxMasterPrio: maxMasterPrioBase,
		rate:          uint16(rand.IntN(rateMax)) + 1,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, event string, state *s.State, notifyEvSrcFunc func(string, m.EventFromReconcile)) (m.Result, error) {
	r.log.Debug("reconciling event", slog.String("event", event), slog.Any("state", state))

	if event == "config-updater" && state.Config.EvalIntv != 0 {
		r.evalIntv = time.Millisecond * time.Duration(state.Config.EvalIntv)
		r.advertIntv = time.Millisecond * time.Duration(state.Config.AdvertIntv)
	}

	if event == "packet-sender" {
		return m.Result{}, nil
	}

	haState := state.HAState
	masterExists := false
	timeNow := time.Now()

	for dst, pkt := range state.PacketRecvMap {
		if !pkt.Reconciled {
			pkt.Reconciled = true

			notifyEvSrcFunc("packet-sender", m.EventFromReconcile{
				Event: m.Event{Data: s.SenderNotifyEvent{
					EventType:     s.PerSenderNotifyEvent,
					DstIP:         dst,
					PeerNum:       pkt.SelfNum,
					PeerNumRxTime: pkt.RecvTime,
				}},
			})

			state.PacketRecvMap[dst] = pkt
		}
	}

	switch haState {
	case s.FaultState:
		// todo: check if all check_scripts have passed;
		// if so promote to backup

	case s.BackupState:
		// todo: check checks scripts; if failed fall back to FaulState

		// Any master exists
		if slices.ContainsFunc(slices.Collect(maps.Values(state.PacketRecvMap)), func(pkt packet.PacketRx) bool {
			if timeNow.Sub(pkt.RecvTime) > validFactor*r.advertIntv {
				return false
			}
			return pkt.IsMaster
		}) {
			masterExists = true
			break
		}

		// Any Peer has high prio
		peerHasHighPrio := false
		selfPrio := r.lastGlobalNotifyEvent.Prio
		for _, pkt := range state.PacketRecvMap {

			if timeNow.Sub(pkt.RecvTime) > validFactor*r.advertIntv {
				continue
			}

			if selfPrio < pkt.Priority {
				peerHasHighPrio = true
				break
			}
		}
		if peerHasHighPrio {
			break
		}

		// Did self cross threshold ?
		if r.riseTimeStart == nil {
			r.riseTimeStart = utils.PtrTo(time.Now())
		}
		if time.Since(*r.riseTimeStart) > thresholdFactor*r.advertIntv {
			state.HAState = s.MasterState
			r.maxMasterPrio = maxMasterPrioBase + uint16(rand.Int32N(100))
			r.riseTimeStart = nil
		}

	case s.MasterState:
		// todo: check checks scripts; if failed fall back to FaulState

		// todo: if any Peer is master, check prio
		// if self > peer: remain master
		// else; drop master
		peerHasHighPrio := false
		selfPrio := r.lastGlobalNotifyEvent.Prio
		for _, pkt := range state.PacketRecvMap {
			if !pkt.IsMaster {
				continue
			}

			if timeNow.Sub(pkt.RecvTime) > validFactor*r.advertIntv {
				continue
			}

			if selfPrio < pkt.Priority {
				peerHasHighPrio = true
				break
			}
		}
		if peerHasHighPrio {
			state.HAState = s.BackupState
			break
		}

		// todo: Are we receiving packets from 1 out of min 2 peers.
		// if 0/2+; drop master; network down
		// if 0/1; remain master
		if len(state.PacketRecvMap) > 1 {
			if !slices.ContainsFunc(slices.Collect(maps.Values(state.PacketRecvMap)), func(pkt packet.PacketRx) bool {
				return timeNow.Sub(pkt.RecvTime) < validFactor*r.advertIntv
			}) {
				state.HAState = s.BackupState
			}
		}

	}

	currGlobalNotifyEvent := s.SenderNotifyEvent{
		EventType: s.GlobalNotifyEvent,
		HAState:   state.HAState,
	}
	switch state.HAState {
	case s.FaultState:
		currGlobalNotifyEvent.Prio = 0
	case s.BackupState:
		if masterExists {
			currGlobalNotifyEvent.Prio = 0
			break
		}

		switch state.PrevHAState {
		case s.BackupState:
			currGlobalNotifyEvent.Prio = (r.lastGlobalNotifyEvent.Prio + r.rate) & uint16(0x7fff)
		default:
			currGlobalNotifyEvent.Prio = 0
			r.rate = uint16(rand.IntN(rateMax)) + 1
		}
	case s.MasterState:
		switch state.PrevHAState {
		case s.MasterState:
			currGlobalNotifyEvent.Prio = utils.Min(r.maxMasterPrio, r.lastGlobalNotifyEvent.Prio+r.rate)
		default:
			currGlobalNotifyEvent.Prio = 0
			r.rate = uint16(rand.IntN(rateMax)) + 1
		}
	}
	state.PrevHAState = state.HAState

	if currGlobalNotifyEvent != r.lastGlobalNotifyEvent {
		if currGlobalNotifyEvent.HAState != r.lastGlobalNotifyEvent.HAState {
			r.log.Warn("HA state change", "old", r.lastGlobalNotifyEvent.HAState.String(),
				"new", currGlobalNotifyEvent.HAState.String())
		}
		notifyEvSrcFunc("packet-sender", m.EventFromReconcile{Event: m.Event{Data: currGlobalNotifyEvent}})
		r.lastGlobalNotifyEvent = currGlobalNotifyEvent
		r.log.Info("changed", "currNotifyEvent", currGlobalNotifyEvent.String())
	}

	return m.Result{RequeueAfter: r.evalIntv}, nil
}
