package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/identw/bird-rtt-keeper/pkg/bird"
	"github.com/identw/bird-rtt-keeper/pkg/ping"
	"github.com/identw/bird-rtt-keeper/pkg/tcpcheck"
	"github.com/identw/bird-rtt-keeper/pkg/types"
)

func main() {
	var mode string
	var portsStr string
	flag.StringVar(&mode, "mode", "bird-rtt-checker", "Mode to run: 'bird-rtt-checker' or 'tcpcheck-server'")
	flag.StringVar(&portsStr, "ports", tcpcheck.DefaultPortStr, "Comma-separated list of ports (e.g., 8080,8081,8082) for server mode")
	flag.Parse()

	if mode == "tcpcheck-server" {
		tcpcheck.Run(tcpcheck.GetPorts(portsStr))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	bc := bird.NewBirdClient("/run/bird/bird.ctl")
	results := make(chan types.Result, 100)
	healthPeers := make(map[string]*HealthPeer)

	err := syncBgpPeers(ctx, bc, healthPeers, results)
	if err != nil {
		log.Printf("Error syncing BGP peers: %v", err)
	}

	// re read BGP peers every 2 minutes
	go func() {
		ticker := time.NewTicker(120 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				err = syncBgpPeers(ctx, bc, healthPeers, results)
				if err != nil {
					log.Printf("Error syncing BGP peers: %v", err)
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		for result := range results {
			if result.Err != nil {
				log.Printf("Result for %s: error: %v", healthPeers[result.IP].BgpPeer.Name, result.Err)
			}
			if !result.Alive && result.Checker == "ping" {
				healthPeers[result.IP].DisablePeer(result.Reason)
			} else if result.Alive && result.Checker == "ping" {
				healthPeers[result.IP].EnablePeer()
			}
			if result.Checker == "tcpcheck" {
				log.Printf("TCP check result for %s: alive: %v, error: %v", healthPeers[result.IP].BgpPeer.Name, result.Alive, result.Err)
			}
		}
	}()

	<-sigChan
	log.Println("\n Stopping...")
	cancel()
	time.Sleep(3 * time.Second)
	close(results)

	log.Println("Done")
}

func syncBgpPeers(ctx context.Context, bc *bird.BirdClient, healthPeers map[string]*HealthPeer, results chan<- types.Result) error {
	bgpPeers, err := bc.ReadBgpPeers()
	if err != nil {
		return fmt.Errorf("read BGP peers: %w", err)
	}
	var peerMaps = make(map[string]struct{})
	for _, peer := range bgpPeers {
		peerMaps[peer.IP] = struct{}{}
	}

	// Remove peers that are no longer present
	for ip, hp := range healthPeers {
		if _, exists := peerMaps[ip]; !exists {
			log.Printf("Removing BGP peer: %s (%s)", hp.BgpPeer.Name, hp.BgpPeer.IP)
			healthPeers[ip].PingerCancel()
			healthPeers[ip].TcpCheckerCancel()
			delete(healthPeers, ip)
		}
	}

	for _, peer := range bgpPeers {
		if _, exists := healthPeers[peer.IP]; exists {
			if peer.State != healthPeers[peer.IP].EnabledPeer {
				healthPeers[peer.IP].EnabledPeer = peer.State
			}
			continue
		}
		log.Printf("Found BGP peer: %s (%s)", peer.Name, peer.IP)
		pingerCtx, pingerCancel := context.WithCancel(ctx)
		pinger := ping.NewPinger(peer.IP)

		tcpCheckerCtx, tcpCheckerCancel := context.WithCancel(ctx)
		tcpChecker := tcpcheck.NewTcpChecker(peer.IP)
		healthPeers[peer.IP] = &HealthPeer{
			Pinger:           pinger,
			PingerCancel:     pingerCancel,
			TcpChecker:       tcpChecker,
			TcpCheckerCancel: tcpCheckerCancel,
			BgpPeer:          peer,
			EnabledPeer:      peer.State,
			BirdClient:       bc,
			PauseDuration:    0,
			PauseSince:       time.Time{},
			History: History{
				Entries: make([]bool, 0, 10),
				Size:    10,
			},
		}
		go healthPeers[peer.IP].Pinger.Run(pingerCtx, results)
		go healthPeers[peer.IP].TcpChecker.Run(tcpCheckerCtx, results)
	}

	return nil
}

type HealthPeer struct {
	Pinger           *ping.Pinger
	PingerCancel     context.CancelFunc
	TcpChecker       *tcpcheck.TcpChecker
	TcpCheckerCancel context.CancelFunc
	BgpPeer          bird.BgpPeer
	BirdClient       *bird.BirdClient
	EnabledPeer      bool
	PauseDuration    time.Duration
	PauseSince       time.Time
	History          History
}

func (hp *HealthPeer) DisablePeer(reason types.Reason) {
	hp.History.AddEntry(false)
	hp.PauseSince = time.Now()
	if !hp.EnabledPeer || !hp.History.FailedFewLastChecks() {
		return
	}
	hp.EnabledPeer = false
	log.Printf("Disable BGP peer %s (%s), reason: %s", hp.BgpPeer.Name, hp.BgpPeer.IP, reason)
	log.Printf("	peer %s, PauseDuration: %v, PauseSince: %s, Pause left (%v)", hp.BgpPeer.Name, hp.PauseDuration, hp.PauseSince.Format(time.RFC3339), hp.PauseDuration-time.Since(hp.PauseSince))
	hp.BirdClient.DisableProtocol(hp.BgpPeer.Name)

	if hp.PauseDuration == 0 {
		hp.PauseDuration = time.Second * 150
	} else {
		hp.PauseDuration = hp.PauseDuration * 2
	}
}

func (hp *HealthPeer) EnablePeer() {
	hp.History.AddEntry(true)
	now := time.Now()
	if !hp.EnabledPeer && (now.Sub(hp.PauseSince) < hp.PauseDuration) {
		log.Printf("	peer %s, PauseDuration: %v, PauseSince: %s, Pause left (%v)", hp.BgpPeer.Name, hp.PauseDuration, hp.PauseSince.Format(time.RFC3339), hp.PauseDuration-time.Since(hp.PauseSince))
		return
	}

	if !hp.EnabledPeer {
		hp.EnabledPeer = true
		log.Printf("Enable BGP peer %s (%s)", hp.BgpPeer.Name, hp.BgpPeer.IP)
		hp.BirdClient.EnableProtocol(hp.BgpPeer.Name)
	}

	if now.Sub(hp.PauseSince) >= time.Minute*45 && hp.History.SuccessChecks() {
		hp.PauseDuration = 0
	}
}

type History struct {
	Entries []bool
	Size    int
}

func (h *History) AddEntry(en bool) {
	if len(h.Entries) >= h.Size {
		h.Entries = h.Entries[1:]
	}
	h.Entries = append(h.Entries, en)
}

func (h *History) SuccessChecks() bool {
	for i := len(h.Entries) - 1; i >= 0; i-- {
		if !h.Entries[i] {
			return false
		}
	}
	return true
}

func (h *History) FailedFewLastChecks() bool {
	fails := 3
	if len(h.Entries) < fails {
		return false
	}
	for i := len(h.Entries) - 1; i >= len(h.Entries)-fails; i-- {
		if h.Entries[i] {
			return false
		}
	}
	return true
}
