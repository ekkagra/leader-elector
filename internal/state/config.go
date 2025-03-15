package state

import "net/netip"

type Addr struct {
	IP   netip.Addr `json:"ip"`
	Port int        `json:"port"`
}

type Addr2 struct {
	Addr
	SrcPort int `json:"src_port"`
}

type Config struct {
	Id         int     `json:"id"`
	ListenAddr Addr    `json:"listen_addr"`
	PeerAddrs  []Addr2 `json:"peer_addrs"`
	AdvertIntv int     `json:"advert_intv"`
	EvalIntv   int     `json:"eval_intv"`
}
