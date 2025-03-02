package utils

import (
	"cmp"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

func AddrPortOrDefaults(s string) (net.IP, uint16) {
	var addrPort netip.AddrPort
	addrPort, err := netip.ParseAddrPort(s)
	if err == nil {
		return addrPort.Addr().AsSlice(), addrPort.Port()
	}

	addr, err := netip.ParseAddr(s)
	if err == nil {
		return addr.AsSlice(), 0
	}

	if idx := strings.LastIndex(s, ":"); idx != -1 && idx < len(s)-1 {
		if p, err := strconv.ParseUint(s[idx+1:], 10, 16); err == nil {
			return nil, uint16(p)
		}

	}
	return nil, 0
}

func Max[T cmp.Ordered](a T, b T) T {
	if a > b {
		return a
	}
	return b
}

func Min[T cmp.Ordered](a T, b T) T {
	if a < b {
		return a
	}
	return b
}

type RingBuffer[T any] struct {
	cap   int
	buf   []*T
	rHead int
	wHead int
}

func NewRingBuffer[T any](cap int) *RingBuffer[T] {
	return &RingBuffer[T]{
		cap:   cap,
		buf:   make([]*T, cap),
		rHead: -1,
		wHead: 0,
	}
}

func (r *RingBuffer[T]) increment(idx, by int) int {
	if idx+by > r.cap-1 {
		return idx + by - r.cap
	}
	return idx + by
}

func (r RingBuffer[T]) decrement(idx, by int) int {
	if idx-by < 0 {
		return idx - by + r.cap
	}
	return idx - by
}

func (r *RingBuffer[T]) Read() (T, bool) {
	var out T
	if r.rHead == -1 {
		return out, false
	}

	out = *r.buf[r.rHead]

	r.rHead = r.increment(r.rHead, 1)
	if r.rHead == r.wHead {
		r.rHead = -1
		r.wHead = 0
	}

	fmt.Println(r.buf)
	return out, true
}

func (r *RingBuffer[T]) Write(ele T) {
	r.buf[r.wHead] = &ele
	if r.wHead == r.rHead {
		r.rHead = r.increment(r.rHead, 1)
	} else if r.rHead == -1 {
		r.rHead = r.wHead
	}
	r.wHead = r.increment(r.wHead, 1)
}

func (r *RingBuffer[T]) Peek(latest bool) (T, bool) {
	var out T
	if r.rHead == -1 {
		return out, false
	}

	if latest {
		return *r.buf[r.decrement(r.wHead, 1)], true
	}
	return *r.buf[r.rHead], true
}

func (r *RingBuffer[T]) Values() []T {
	out := make([]T, 0, r.cap)

	if r.rHead == -1 {
		return out
	}

	till := r.wHead
	if r.wHead <= r.rHead {
		till = r.wHead + r.cap
	}

	rhead := r.rHead
	for i := r.rHead; i < till; i++ {
		out = append(out, *r.buf[rhead])
		rhead = r.increment(rhead, 1)
	}
	return out
}

func PtrTo[T any](val T) *T {
	return &val
}
