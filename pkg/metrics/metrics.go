package metrics

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const DefaultListenAddr = "127.0.0.1:9574"

// PeerSnapshot holds the current metric values for one BGP peer.
type PeerSnapshot struct {
	Name            string
	IP              string
	IcmpAlive       bool
	TcpActualAlive  bool
	BgpSessionUp    bool
	BfdSessionUp    bool
	HasBfd          bool
	TcpCheckEnabled bool
	TcpCheckEnforce bool

	PeerEnabled           bool
	PauseRemainingSeconds float64
	IcmpConsecFails       int
	IcmpConsecSuccesses   int
	TcpConsecFails        int
	TcpConsecSuccesses    int
	LastDisableReason     string

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

	BgpPrefixesImported float64
	BgpPrefixesExported float64

	BfdIntervalSeconds float64
	BfdTimeoutSeconds  float64
}

// Exporter serves Prometheus metrics for bird-rtt-keeper.
type Exporter struct {
	listenAddr string

	hostAlive             *prometheus.GaugeVec
	icmpAlive             *prometheus.GaugeVec
	tcpAlive              *prometheus.GaugeVec
	bgpSessionUp          *prometheus.GaugeVec
	bfdSessionUp          *prometheus.GaugeVec
	peerEnabled           *prometheus.GaugeVec
	pauseRemaining        *prometheus.GaugeVec
	consecutiveFailures   *prometheus.GaugeVec
	consecutiveSuccesses  *prometheus.GaugeVec
	lastDisableReason     *prometheus.GaugeVec
	icmpPacketLoss        *prometheus.GaugeVec
	icmpRttSeconds        *prometheus.GaugeVec
	tcpDurationSeconds    *prometheus.GaugeVec
	tcpThroughput         *prometheus.GaugeVec
	lastCheckTimestamp    *prometheus.GaugeVec
	bgpPrefixesImported   *prometheus.GaugeVec
	bgpPrefixesExported   *prometheus.GaugeVec
	bfdIntervalSeconds    *prometheus.GaugeVec
	bfdTimeoutSeconds     *prometheus.GaugeVec
	peerInfo              *prometheus.GaugeVec
	configInfo            *prometheus.GaugeVec

	mu          sync.RWMutex
	lastReasons map[string]string
}

// New creates a metrics exporter that listens on listenAddr.
func New(listenAddr string) *Exporter {
	e := &Exporter{
		listenAddr:  listenAddr,
		lastReasons: make(map[string]string),
	}

	peerLabels := []string{"peer", "peer_ip"}

	e.hostAlive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_host_alive",
		Help: "Whether the host passes all enabled health checks (1=alive, 0=dead).",
	}, peerLabels)

	e.icmpAlive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_icmp_alive",
		Help: "Whether the last ICMP check succeeded (1=alive, 0=dead).",
	}, peerLabels)

	e.tcpAlive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_tcp_alive",
		Help: "Whether the last TCP check succeeded (1=alive, 0=dead). Exported only when TCP check is enabled.",
	}, peerLabels)

	e.bgpSessionUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_bgp_session_up",
		Help: "Whether the BGP session with the peer is established (1=up, 0=down).",
	}, peerLabels)

	e.bfdSessionUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_bfd_session_up",
		Help: "Whether the BFD session with the peer is up (1=up, 0=down). Exported only when a BFD session exists.",
	}, peerLabels)

	e.peerEnabled = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_peer_enabled",
		Help: "Whether the keeper has the BGP protocol enabled in bird (1=enabled, 0=disabled by keeper).",
	}, peerLabels)

	e.pauseRemaining = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_pause_remaining_seconds",
		Help: "Seconds remaining before the keeper may re-enable a disabled peer.",
	}, peerLabels)

	e.consecutiveFailures = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_consecutive_failures",
		Help: "Number of consecutive failed health checks.",
	}, append(peerLabels, "check"))

	e.consecutiveSuccesses = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_consecutive_successes",
		Help: "Number of consecutive successful health checks.",
	}, append(peerLabels, "check"))

	e.lastDisableReason = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_last_disable_reason_info",
		Help: "Last reason the keeper disabled the peer (value is always 1).",
	}, append(peerLabels, "reason"))

	e.icmpPacketLoss = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_icmp_packet_loss_ratio",
		Help: "Packet loss ratio from the last ICMP check (0-100).",
	}, peerLabels)

	e.icmpRttSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_icmp_rtt_seconds",
		Help: "RTT from the last ICMP check in seconds.",
	}, append(peerLabels, "stat"))

	e.tcpDurationSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_tcp_duration_seconds",
		Help: "Transfer duration from the last TCP check in seconds.",
	}, append(peerLabels, "stat"))

	e.tcpThroughput = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_tcp_throughput_bytes_per_second",
		Help: "Average throughput from the last TCP check in bytes per second.",
	}, peerLabels)

	e.lastCheckTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_last_check_timestamp",
		Help: "Unix timestamp of the last health check.",
	}, append(peerLabels, "check"))

	e.bgpPrefixesImported = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_bgp_prefixes_imported",
		Help: "Number of routes imported from the BGP peer.",
	}, peerLabels)

	e.bgpPrefixesExported = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_bgp_prefixes_exported",
		Help: "Number of routes exported to the BGP peer.",
	}, peerLabels)

	e.bfdIntervalSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_bfd_interval_seconds",
		Help: "BFD transmit interval in seconds. Exported only when a BFD session exists.",
	}, peerLabels)

	e.bfdTimeoutSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_bfd_timeout_seconds",
		Help: "BFD detection timeout in seconds. Exported only when a BFD session exists.",
	}, peerLabels)

	e.peerInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_peer_info",
		Help: "Peer metadata (value is always 1). Label vpn is parsed from the peer name suffix after the last underscore.",
	}, append(peerLabels, "vpn"))

	e.configInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bird_rtt_keeper_config_info",
		Help: "Active health-check configuration for this keeper instance (value is always 1).",
	}, []string{"icmp_check", "tcpcheck", "tcpcheck_enforce"})

	prometheus.MustRegister(
		e.hostAlive,
		e.icmpAlive,
		e.tcpAlive,
		e.bgpSessionUp,
		e.bfdSessionUp,
		e.peerEnabled,
		e.pauseRemaining,
		e.consecutiveFailures,
		e.consecutiveSuccesses,
		e.lastDisableReason,
		e.icmpPacketLoss,
		e.icmpRttSeconds,
		e.tcpDurationSeconds,
		e.tcpThroughput,
		e.lastCheckTimestamp,
		e.bgpPrefixesImported,
		e.bgpPrefixesExported,
		e.bfdIntervalSeconds,
		e.bfdTimeoutSeconds,
		e.peerInfo,
		e.configInfo,
	)

	return e
}

// SetConfig publishes the keeper health-check configuration as an info metric.
func (e *Exporter) SetConfig(tcpCheckEnabled, tcpCheckEnforce bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.configInfo.Reset()
	e.configInfo.WithLabelValues(
		"true",
		boolLabel(tcpCheckEnabled),
		boolLabel(tcpCheckEnforce),
	).Set(1)
}

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Start runs the HTTP metrics server until ctx is cancelled.
func (e *Exporter) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:    e.listenAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("Metrics server listening on http://%s/metrics", e.listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Metrics server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Printf("Metrics server shutdown error: %v", err)
		}
	}()
}

// UpdatePeer refreshes all gauge values for a peer.
func (e *Exporter) UpdatePeer(ps PeerSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()

	labels := []string{ps.Name, ps.IP}

	e.icmpAlive.WithLabelValues(labels...).Set(boolToFloat(ps.IcmpAlive))
	e.bgpSessionUp.WithLabelValues(labels...).Set(boolToFloat(ps.BgpSessionUp))
	e.peerEnabled.WithLabelValues(labels...).Set(boolToFloat(ps.PeerEnabled))
	e.pauseRemaining.WithLabelValues(labels...).Set(ps.PauseRemainingSeconds)
	e.bgpPrefixesImported.WithLabelValues(labels...).Set(ps.BgpPrefixesImported)
	e.bgpPrefixesExported.WithLabelValues(labels...).Set(ps.BgpPrefixesExported)
	e.peerInfo.WithLabelValues(ps.Name, ps.IP, VPNFromPeer(ps.Name)).Set(1)

	e.consecutiveFailures.WithLabelValues(ps.Name, ps.IP, "icmp").Set(float64(ps.IcmpConsecFails))
	e.consecutiveFailures.WithLabelValues(ps.Name, ps.IP, "tcp").Set(float64(ps.TcpConsecFails))
	e.consecutiveSuccesses.WithLabelValues(ps.Name, ps.IP, "icmp").Set(float64(ps.IcmpConsecSuccesses))
	e.consecutiveSuccesses.WithLabelValues(ps.Name, ps.IP, "tcp").Set(float64(ps.TcpConsecSuccesses))

	e.setLastDisableReason(ps)

	if ps.TcpCheckEnabled {
		e.tcpAlive.WithLabelValues(labels...).Set(boolToFloat(ps.TcpActualAlive))
	} else {
		e.tcpAlive.DeleteLabelValues(labels...)
	}

	if ps.HasBfd {
		e.bfdSessionUp.WithLabelValues(labels...).Set(boolToFloat(ps.BfdSessionUp))
		e.bfdIntervalSeconds.WithLabelValues(labels...).Set(ps.BfdIntervalSeconds)
		e.bfdTimeoutSeconds.WithLabelValues(labels...).Set(ps.BfdTimeoutSeconds)
	} else {
		e.bfdSessionUp.DeleteLabelValues(labels...)
		e.bfdIntervalSeconds.DeleteLabelValues(labels...)
		e.bfdTimeoutSeconds.DeleteLabelValues(labels...)
	}

	e.hostAlive.WithLabelValues(labels...).Set(boolToFloat(hostAliveFromChecks(
		ps.IcmpAlive,
		ps.TcpActualAlive,
		ps.TcpCheckEnabled,
		ps.TcpCheckEnforce,
	)))

	if ps.HasIcmpStats {
		e.icmpPacketLoss.WithLabelValues(labels...).Set(ps.IcmpPacketLoss)
		setLabeledStats(e.icmpRttSeconds, labels, map[string]float64{
			"avg":    ps.IcmpRttAvg,
			"min":    ps.IcmpRttMin,
			"max":    ps.IcmpRttMax,
			"stddev": ps.IcmpRttStdDev,
		})
	}
	if !ps.IcmpLastCheck.IsZero() {
		e.lastCheckTimestamp.WithLabelValues(ps.Name, ps.IP, "icmp").Set(float64(ps.IcmpLastCheck.Unix()))
	}

	if ps.HasTcpStats {
		setLabeledStats(e.tcpDurationSeconds, labels, map[string]float64{
			"avg": ps.TcpDurationAvg,
			"min": ps.TcpDurationMin,
			"max": ps.TcpDurationMax,
		})
		e.tcpThroughput.WithLabelValues(labels...).Set(ps.TcpThroughputBytesPerSec)
	} else if ps.TcpCheckEnabled {
		e.tcpDurationSeconds.DeleteLabelValues(ps.Name, ps.IP, "avg")
		e.tcpDurationSeconds.DeleteLabelValues(ps.Name, ps.IP, "min")
		e.tcpDurationSeconds.DeleteLabelValues(ps.Name, ps.IP, "max")
		e.tcpThroughput.DeleteLabelValues(labels...)
	}
	if !ps.TcpLastCheck.IsZero() {
		e.lastCheckTimestamp.WithLabelValues(ps.Name, ps.IP, "tcp").Set(float64(ps.TcpLastCheck.Unix()))
	}
}

func (e *Exporter) setLastDisableReason(ps PeerSnapshot) {
	reason := ps.LastDisableReason
	if reason == "" {
		reason = "none"
	}

	key := ps.Name + "|" + ps.IP
	if prev, ok := e.lastReasons[key]; ok && prev != reason {
		e.lastDisableReason.DeleteLabelValues(ps.Name, ps.IP, prev)
	}
	e.lastReasons[key] = reason
	e.lastDisableReason.WithLabelValues(ps.Name, ps.IP, reason).Set(1)
}

func setLabeledStats(vec *prometheus.GaugeVec, labels []string, stats map[string]float64) {
	for stat, value := range stats {
		vec.WithLabelValues(append(append([]string{}, labels...), stat)...).Set(value)
	}
}

func hostAliveFromChecks(icmpAlive, tcpActualAlive, tcpCheckEnabled, tcpCheckEnforce bool) bool {
	hostAlive := icmpAlive
	if tcpCheckEnabled && tcpCheckEnforce {
		hostAlive = hostAlive && tcpActualAlive
	}
	return hostAlive
}

// RemovePeer deletes all metrics for a peer that is no longer monitored.
func (e *Exporter) RemovePeer(name, ip string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.deletePeerMetrics(name, ip)
}

func (e *Exporter) deletePeerMetrics(name, ip string) {
	key := name + "|" + ip
	if reason, ok := e.lastReasons[key]; ok {
		e.lastDisableReason.DeleteLabelValues(name, ip, reason)
		delete(e.lastReasons, key)
	}

	labels := []string{name, ip}
	e.hostAlive.DeleteLabelValues(labels...)
	e.icmpAlive.DeleteLabelValues(labels...)
	e.tcpAlive.DeleteLabelValues(labels...)
	e.bgpSessionUp.DeleteLabelValues(labels...)
	e.bfdSessionUp.DeleteLabelValues(labels...)
	e.peerEnabled.DeleteLabelValues(labels...)
	e.pauseRemaining.DeleteLabelValues(labels...)
	e.icmpPacketLoss.DeleteLabelValues(labels...)
	e.tcpThroughput.DeleteLabelValues(labels...)
	e.bgpPrefixesImported.DeleteLabelValues(labels...)
	e.bgpPrefixesExported.DeleteLabelValues(labels...)
	e.bfdIntervalSeconds.DeleteLabelValues(labels...)
	e.bfdTimeoutSeconds.DeleteLabelValues(labels...)
	e.peerInfo.DeleteLabelValues(name, ip, VPNFromPeer(name))

	for _, check := range []string{"icmp", "tcp"} {
		e.consecutiveFailures.DeleteLabelValues(name, ip, check)
		e.consecutiveSuccesses.DeleteLabelValues(name, ip, check)
		e.lastCheckTimestamp.DeleteLabelValues(name, ip, check)
	}
	for _, stat := range []string{"avg", "min", "max", "stddev"} {
		e.icmpRttSeconds.DeleteLabelValues(name, ip, stat)
	}
	for _, stat := range []string{"avg", "min", "max"} {
		e.tcpDurationSeconds.DeleteLabelValues(name, ip, stat)
	}
}

func boolToFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
