package utils

import (
	"cmp"
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
