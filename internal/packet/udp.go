package packet

import (
	"log/slog"
	"mux/internal/utils"
	"net"
	"time"
)

const (
	intervalMs = 500
)

func GenerateNoConn(log *slog.Logger, selfIPPort, remoteIPPort string) {

	log.Info("selfIP", slog.String("ip", selfIPPort))

	srcIP, srcPort := utils.AddrPortOrDefaults(selfIPPort)
	src := &net.UDPAddr{IP: srcIP, Port: int(srcPort)}

	destIP, destPort := utils.AddrPortOrDefaults(remoteIPPort)
	dest := &net.UDPAddr{IP: destIP, Port: int(destPort)}

	conn, err := net.ListenUDP("udp", src)
	if err != nil {
		log.Error("error while dialing", slog.Any("err", err))
		return
	}
	defer conn.Close()
	log.Info("dialed ok, got conn", slog.Any("conn", conn))

	for {
		n, err := conn.WriteToUDP([]byte("hello world 1"), dest)
		if err != nil {
			log.Error("error while writing", slog.Any("err", err))
			return
		}
		log.Info("wrote bytes", slog.Any("n", n))
		time.Sleep(time.Millisecond * intervalMs)
	}
}

func GenerateConn(log *slog.Logger, selfIPPort, remoteIPPort string) {
	log.Info("selfIPPort", slog.String("ip", selfIPPort))

	srcIP, srcPort := utils.AddrPortOrDefaults(selfIPPort)
	src := &net.UDPAddr{IP: srcIP, Port: int(srcPort)}

	destIP, destPort := utils.AddrPortOrDefaults(remoteIPPort)
	dest := &net.UDPAddr{IP: destIP, Port: int(destPort)}

	conn, err := net.DialUDP("udp", src, dest)
	if err != nil {
		log.Error("error while dialing", slog.Any("err", err))
		return
	}
	defer conn.Close()
	log.Info("dialed ok, got conn", slog.Any("conn", conn))

	for {
		n, err := conn.Write([]byte("hello world 1"))
		if err != nil {
			log.Error("error while writing", slog.Any("err", err))
			return
		}
		log.Info("wrote bytes", slog.Any("n", n))
		time.Sleep(time.Millisecond * intervalMs)
	}
}
