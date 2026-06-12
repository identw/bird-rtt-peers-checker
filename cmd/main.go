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
	"github.com/identw/bird-rtt-keeper/pkg/metrics"
	"github.com/identw/bird-rtt-keeper/pkg/ping"
	"github.com/identw/bird-rtt-keeper/pkg/tcpcheck"
	"github.com/identw/bird-rtt-keeper/pkg/types"
)

var (
	mode            string
	portsStr        string
	tcpcheckEnabled bool
	tcpcheckEnforce bool
	metricsEnabled  bool
	metricsListen   string
)

func main() {

	flag.StringVar(&mode, "mode", "bird-rtt-checker", "Mode to run: 'bird-rtt-checker' or 'tcpcheck-server'")
	flag.StringVar(&portsStr, "ports", tcpcheck.DefaultPortStr, "Comma-separated list of ports (e.g., 8080,8081,8082) for server mode")
	flag.BoolVar(&tcpcheckEnabled, "tcpcheck", true, "Enable TCP check")
	flag.BoolVar(&tcpcheckEnforce, "tcpcheck-enforce", false, "Enforce TCP check")
	flag.BoolVar(&metricsEnabled, "metrics", true, "Enable Prometheus metrics exporter")
	flag.StringVar(&metricsListen, "metrics-listen", metrics.DefaultListenAddr, "Prometheus metrics listen address")
	flag.Parse()

	if tcpcheckEnforce && !tcpcheckEnabled {
		log.Fatal("--tcpcheck-enforce requires --tcpcheck")
	}

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

	var exporter *metrics.Exporter
	if metricsEnabled {
		exporter = metrics.New(metricsListen)
		exporter.SetConfig(tcpcheckEnabled, tcpcheckEnforce)
		exporter.Start(ctx)
	}

	err := syncBgpPeers(ctx, bc, healthPeers, results, exporter)
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
				err = syncBgpPeers(ctx, bc, healthPeers, results, exporter)
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
			hp, ok := healthPeers[result.IP]
			if !ok {
				continue
			}

			if result.Err != nil {
				log.Printf("Result for %s [%s]: error: %v", hp.BgpPeer.Name, result.Checker, result.Err)
			}

			// Record result in the appropriate history
			switch result.Checker {
			case "ping":
				hp.IcmpHistory.Record(result.Alive)
				if result.Icmp != nil {
					hp.HasIcmpStats = true
					hp.IcmpPacketLoss = result.Icmp.PacketLoss
					hp.IcmpRttAvg = result.Icmp.AvgRtt.Seconds()
					hp.IcmpRttMin = result.Icmp.MinRtt.Seconds()
					hp.IcmpRttMax = result.Icmp.MaxRtt.Seconds()
					hp.IcmpRttStdDev = result.Icmp.StdDevRtt.Seconds()
				}
				hp.IcmpLastCheck = result.Timestamp
			case "tcpcheck":
				hp.TcpActualAlive = result.Alive
				hp.TcpActualHasData = true
				hp.recordTcpActual(result.Alive)
				if result.Tcp != nil {
					hp.HasTcpStats = true
					hp.TcpDurationAvg = result.Tcp.AvgDuration.Seconds()
					hp.TcpDurationMin = result.Tcp.MinDuration.Seconds()
					hp.TcpDurationMax = result.Tcp.MaxDuration.Seconds()
					hp.TcpThroughputBytesPerSec = result.Tcp.ThroughputBytesPerSec
				}
				hp.TcpLastCheck = result.Timestamp
				if !result.Alive {
					log.Printf("TCP check result for %s: alive: %v, reason: %s", healthPeers[result.IP].BgpPeer.Name, result.Alive, result.Reason)
				}
				if tcpcheckEnforce {
					if !result.Alive {
						log.Printf("	tcp record for %s: alive: %v, reason: %s", healthPeers[result.IP].BgpPeer.Name, result.Alive, result.Reason)
					}
					hp.TcpHistory.Record(result.Alive)
				} else {
					hp.TcpHistory.Record(true)
				}
			}
			if !result.Alive {
				hp.TcpHistory.PrintStats(fmt.Sprintf("TCP stats for %s: ", healthPeers[result.IP].BgpPeer.Name))
				hp.IcmpHistory.PrintStats(fmt.Sprintf("ICMP stats for %s: ", healthPeers[result.IP].BgpPeer.Name))
			}

			// Decide whether to disable or enable the peer based on combined state
			icmpFailing := hp.IcmpHistory.FailedFewLastChecks()
			tcpFailing := hp.TcpHistory.FailedFewLastChecks()

			if icmpFailing || tcpFailing {
				hp.DisablePeer(result.Reason)
			} else if hp.IcmpHistory.LastCheckAlive() && hp.TcpHistory.LastCheckAlive() {
				hp.EnablePeer()
			}

			if exporter != nil {
				exporter.UpdatePeer(hp.metricsSnapshot(tcpcheckEnabled, tcpcheckEnforce))
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

func syncBgpPeers(ctx context.Context, bc *bird.BirdClient, healthPeers map[string]*HealthPeer, results chan<- types.Result, exporter *metrics.Exporter) error {
	bgpPeers, err := bc.ReadBgpPeers()
	if err != nil {
		return fmt.Errorf("read BGP peers: %w", err)
	}

	bfdSessions, err := bc.ReadBfdSessions()
	if err != nil {
		log.Printf("Error reading BFD sessions: %v", err)
	}
	bfdByIP := bird.BfdSessionsByIP(bfdSessions)

	var peerMaps = make(map[string]struct{})
	for _, peer := range bgpPeers {
		peerMaps[peer.IP] = struct{}{}
	}

	// Remove peers that are no longer present
	for ip, hp := range healthPeers {
		if _, exists := peerMaps[ip]; !exists {
			log.Printf("Removing BGP peer: %s (%s)", hp.BgpPeer.Name, hp.BgpPeer.IP)
			if exporter != nil {
				exporter.RemovePeer(hp.BgpPeer.Name, hp.BgpPeer.IP)
			}
			healthPeers[ip].PingerCancel()
			healthPeers[ip].TcpCheckerCancel()
			delete(healthPeers, ip)
		}
	}

	for _, peer := range bgpPeers {
		if hp, exists := healthPeers[peer.IP]; exists {
			if peer.State != hp.EnabledPeer {
				hp.EnabledPeer = peer.State
			}
			hp.BgpPeer.Name = peer.Name
			hp.BgpPeer.State = peer.State
			hp.BgpPeer.PrefixesImported = peer.PrefixesImported
			hp.BgpPeer.PrefixesExported = peer.PrefixesExported
			if bfd, ok := bfdByIP[peer.IP]; ok {
				hp.HasBfd = true
				hp.BfdSessionUp = bfd.State
				hp.BfdIntervalSeconds = bfd.Interval
				hp.BfdTimeoutSeconds = bfd.Timeout
			} else {
				hp.HasBfd = false
				hp.BfdSessionUp = false
				hp.BfdIntervalSeconds = 0
				hp.BfdTimeoutSeconds = 0
			}
			if exporter != nil {
				exporter.UpdatePeer(hp.metricsSnapshot(tcpcheckEnabled, tcpcheckEnforce))
			}
			continue
		}
		log.Printf("Found BGP peer: %s (%s)", peer.Name, peer.IP)
		pingerCtx, pingerCancel := context.WithCancel(ctx)
		pinger := ping.NewPinger(peer.IP)

		tcpCheckerCtx, tcpCheckerCancel := context.WithCancel(ctx)
		tcpChecker := tcpcheck.NewTcpChecker(peer.IP)

		hp := &HealthPeer{
			Pinger:           pinger,
			PingerCancel:     pingerCancel,
			TcpChecker:       tcpChecker,
			TcpCheckerCancel: tcpCheckerCancel,
			BgpPeer:          peer,
			EnabledPeer:      peer.State,
			BirdClient:       bc,
			PauseDuration:    0,
			PauseSince:       time.Time{},
			IcmpHistory: History{
				FailThreshold:    3,
				SuccessThreshold: 8,
			},
			TcpHistory: History{
				FailThreshold:    2,
				SuccessThreshold: 4,
			},
		}
		if bfd, ok := bfdByIP[peer.IP]; ok {
			hp.HasBfd = true
			hp.BfdSessionUp = bfd.State
			hp.BfdIntervalSeconds = bfd.Interval
			hp.BfdTimeoutSeconds = bfd.Timeout
		}
		healthPeers[peer.IP] = hp
		go healthPeers[peer.IP].Pinger.Run(pingerCtx, results)
		if tcpcheckEnabled {
			go healthPeers[peer.IP].TcpChecker.Run(tcpCheckerCtx, results)
		}
		if exporter != nil {
			exporter.UpdatePeer(hp.metricsSnapshot(tcpcheckEnabled, tcpcheckEnforce))
		}
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
	IcmpHistory      History
	TcpHistory       History
	TcpActualAlive   bool
	TcpActualHasData bool
	TcpConsecFails   int
	TcpConsecSuccesses int
	HasBfd           bool
	BfdSessionUp     bool
	BfdIntervalSeconds float64
	BfdTimeoutSeconds  float64
	LastDisableReason  string

	HasIcmpStats   bool
	IcmpPacketLoss float64
	IcmpRttAvg     float64
	IcmpRttMin     float64
	IcmpRttMax     float64
	IcmpRttStdDev  float64
	IcmpLastCheck  time.Time

	HasTcpStats              bool
	TcpDurationAvg           float64
	TcpDurationMin           float64
	TcpDurationMax           float64
	TcpThroughputBytesPerSec float64
	TcpLastCheck             time.Time
}

func (hp *HealthPeer) metricsSnapshot(tcpCheckEnabled, tcpCheckEnforce bool) metrics.PeerSnapshot {
	return metrics.PeerSnapshot{
		Name:            hp.BgpPeer.Name,
		IP:              hp.BgpPeer.IP,
		IcmpAlive:       hp.IcmpHistory.LastCheckAlive(),
		TcpActualAlive:  hp.lastTcpActualAlive(),
		BgpSessionUp:    hp.BgpPeer.State,
		BfdSessionUp:    hp.BfdSessionUp,
		HasBfd:          hp.HasBfd,
		TcpCheckEnabled: tcpCheckEnabled,
		TcpCheckEnforce: tcpCheckEnforce,

		PeerEnabled:           hp.EnabledPeer,
		PauseRemainingSeconds: hp.pauseRemainingSeconds(),
		IcmpConsecFails:       hp.IcmpHistory.ConsecFails,
		IcmpConsecSuccesses:   hp.IcmpHistory.ConsecSuccesses,
		TcpConsecFails:        hp.TcpConsecFails,
		TcpConsecSuccesses:    hp.TcpConsecSuccesses,
		LastDisableReason:     hp.LastDisableReason,

		HasIcmpStats:   hp.HasIcmpStats,
		IcmpPacketLoss: hp.IcmpPacketLoss,
		IcmpRttAvg:     hp.IcmpRttAvg,
		IcmpRttMin:     hp.IcmpRttMin,
		IcmpRttMax:     hp.IcmpRttMax,
		IcmpRttStdDev:  hp.IcmpRttStdDev,
		IcmpLastCheck:  hp.IcmpLastCheck,

		HasTcpStats:              hp.HasTcpStats,
		TcpDurationAvg:           hp.TcpDurationAvg,
		TcpDurationMin:           hp.TcpDurationMin,
		TcpDurationMax:           hp.TcpDurationMax,
		TcpThroughputBytesPerSec: hp.TcpThroughputBytesPerSec,
		TcpLastCheck:             hp.TcpLastCheck,

		BgpPrefixesImported: float64(hp.BgpPeer.PrefixesImported),
		BgpPrefixesExported: float64(hp.BgpPeer.PrefixesExported),
		BfdIntervalSeconds:  hp.BfdIntervalSeconds,
		BfdTimeoutSeconds:   hp.BfdTimeoutSeconds,
	}
}

func (hp *HealthPeer) pauseRemainingSeconds() float64 {
	if hp.EnabledPeer || hp.PauseDuration == 0 {
		return 0
	}
	remaining := hp.PauseDuration - time.Since(hp.PauseSince)
	if remaining < 0 {
		return 0
	}
	return remaining.Seconds()
}

func (hp *HealthPeer) recordTcpActual(alive bool) {
	if alive {
		hp.TcpConsecSuccesses++
		hp.TcpConsecFails = 0
	} else {
		hp.TcpConsecFails++
		hp.TcpConsecSuccesses = 0
	}
}

func (hp *HealthPeer) lastTcpActualAlive() bool {
	if !hp.TcpActualHasData {
		return true
	}
	return hp.TcpActualAlive
}

func (hp *HealthPeer) DisablePeer(reason types.Reason) {
	hp.PauseSince = time.Now()
	hp.LastDisableReason = string(reason)
	if !hp.EnabledPeer {
		return
	}
	hp.EnabledPeer = false
	log.Printf("Disable BGP peer %s (%s), reason: %s", hp.BgpPeer.Name, hp.BgpPeer.IP, reason)
	log.Printf("	peer %s, PauseDuration: %v, PauseSince: %s, Pause left (%v)", hp.BgpPeer.Name, hp.PauseDuration, hp.PauseSince.Format(time.RFC3339), hp.PauseDuration-time.Since(hp.PauseSince))
	if err := hp.BirdClient.DisableProtocol(hp.BgpPeer.Name); err != nil {
		log.Printf("Error disabling BGP peer %s: %v", hp.BgpPeer.Name, err)
	}

	if hp.PauseDuration == 0 {
		hp.PauseDuration = time.Second * 150
	} else {
		hp.PauseDuration = hp.PauseDuration * 2
	}
}

func (hp *HealthPeer) EnablePeer() {
	now := time.Now()
	if !hp.EnabledPeer && (now.Sub(hp.PauseSince) < hp.PauseDuration) {
		log.Printf("\tpeer %s, PauseDuration: %v, PauseSince: %s, Pause left (%v)", hp.BgpPeer.Name, hp.PauseDuration, hp.PauseSince.Format(time.RFC3339), hp.PauseDuration-time.Since(hp.PauseSince))
		return
	}

	if !hp.EnabledPeer {
		hp.EnabledPeer = true
		log.Printf("Enable BGP peer %s (%s)", hp.BgpPeer.Name, hp.BgpPeer.IP)
		if err := hp.BirdClient.EnableProtocol(hp.BgpPeer.Name); err != nil {
			log.Printf("Error enabling BGP peer %s: %v", hp.BgpPeer.Name, err)
		}
	}

	if now.Sub(hp.PauseSince) >= time.Minute*45 && hp.IcmpHistory.SuccessChecks() && hp.TcpHistory.SuccessChecks() {
		hp.PauseDuration = 0
	}
}

type History struct {
	FailThreshold    int // consecutive failures needed to consider checks failing
	SuccessThreshold int // consecutive successes needed to consider checks recovered
	ConsecFails      int
	ConsecSuccesses  int
	HasData          bool
	LastAlive        bool
}

func (h *History) Record(alive bool) {
	h.HasData = true
	h.LastAlive = alive
	if alive {
		h.ConsecSuccesses++
		h.ConsecFails = 0
	} else {
		h.ConsecFails++
		h.ConsecSuccesses = 0
	}
}

func (h *History) FailedFewLastChecks() bool {
	return h.ConsecFails >= h.FailThreshold
}

func (h *History) SuccessChecks() bool {
	return h.ConsecSuccesses >= h.SuccessThreshold
}

// LastCheckAlive returns true if the last check was successful,
// or true if there is no data yet (no reason to block).
func (h *History) LastCheckAlive() bool {
	if !h.HasData {
		return true
	}
	return h.LastAlive
}

func (h *History) PrintStats(prefix string) {
	log.Printf("%sHistory stats - FailThreshold: %d, SuccessThreshold: %d, ConsecFails: %d, ConsecSuccesses: %d, HasData: %v, LastAlive: %v",
		prefix, h.FailThreshold, h.SuccessThreshold, h.ConsecFails, h.ConsecSuccesses, h.HasData, h.LastAlive)

}
