package k8s

import (
	"context"
	"encoding/json"
	"log/slog"
	"mux/internal/state"
	"mux/internal/utils"
	"net/netip"
	"os"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func initialConfig() state.Config {
	return state.Config{
		Id:         10,
		AdvertIntv: 500,
		EvalIntv:   100,
		ListenAddr: state.Addr{
			Port: 5000,
		},
		PeerAddrs: []state.Addr2{},
	}
}

func Watch(ctx context.Context, log *slog.Logger, path string) {
	log.Info("watcher entered")
	hostName := os.Getenv("HOSTNAME")

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Error("unable to create in-cluster config", slog.Any("err", err))
		return
	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error("unable to create clientset", slog.Any("err", err))
		return
	}

	watcher, err := clientset.CoreV1().Pods("default").Watch(ctx,
		metav1.ListOptions{
			LabelSelector:        "app=leader-elector",
			ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			SendInitialEvents:    utils.PtrTo(true)})
	if err != nil {
		log.Error("unable to create watch", slog.Any("err", err))
		return
	}
	defer watcher.Stop()

	log.Info("starting pod-watcher")

	cfg := initialConfig()
	sourcePortMap := make(map[int]struct{})
	for ev := range watcher.ResultChan() {
		render := false

		pod, ok := ev.Object.(*corev1.Pod)
		if !ok {
			continue
		}

		if pod.Status.PodIP == "" {
			continue
		}

		switch ev.Type {
		case watch.Added, watch.Modified, watch.Bookmark:
			if pod.Name == hostName && pod.Name != cfg.ListenAddr.IP.String() {
				render = true
				cfg.ListenAddr.IP, _ = netip.ParseAddr(pod.Status.PodIP)
			} else {
				if !slices.ContainsFunc(cfg.PeerAddrs, func(existingPeer state.Addr2) bool {
					return existingPeer.IP.String() == pod.Status.PodIP
				}) {
					render = true
					peerIP, _ := netip.ParseAddr(pod.Status.PodIP)
					addr := state.Addr2{
						Addr:    state.Addr{IP: peerIP, Port: 5000},
						SrcPort: getNextAvailablePort(sourcePortMap, 5001),
					}
					cfg.PeerAddrs = append(cfg.PeerAddrs, addr)
				}
			}
		case watch.Deleted:
			idx := slices.IndexFunc(cfg.PeerAddrs, func(existingPeer state.Addr2) bool {
				return existingPeer.IP.String() == pod.Status.PodIP
			})
			if idx != -1 {
				render = true
				toDeletePeer := cfg.PeerAddrs[idx]
				delete(sourcePortMap, toDeletePeer.SrcPort)
				cfg.PeerAddrs = append(cfg.PeerAddrs[0:idx], cfg.PeerAddrs[idx+1:]...)
			}
		}

		if !cfg.ListenAddr.IP.IsValid() {
			continue
		}

		if render {
			log.Info("rendering", slog.Any("cfg", cfg))
			data, err := json.Marshal(cfg)
			if err != nil {
				log.Error("unable to marshal json data", slog.Any("err", err))
				continue
			}
			err = os.WriteFile(path+"_tmp", data, 0744)
			if err != nil {
				log.Error("unable to write file", slog.Any("err", err))
				continue
			}
			err = os.Rename(path+"_tmp", path)
			if err != nil {
				log.Error("unable to rename file", slog.Any("err", err))
				continue
			}
		}
	}
}

func getNextAvailablePort(used map[int]struct{}, start int) int {
	for p := start; ; p++ {
		_, ok := used[p]
		if !ok {
			used[p] = struct{}{}
			return p
		}
	}
}
