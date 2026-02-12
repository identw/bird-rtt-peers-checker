package ping

import (
	"context"
	"fmt"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/identw/bird-rtt-keeper/pkg/types"
)

type Pinger struct {
	IP            string
	PauseActive   bool
	PauseDuration time.Duration
	PauseSince    time.Time
}

const (
	PacketLoss    types.Reason = "[icmp check] packet loss"
	HighAvgRtt    types.Reason = "[icmp check] high average RTT"
	HighMaxRtt    types.Reason = "[icmp check] high maximum RTT"
	HighMinRtt    types.Reason = "[icmp check] high minimum RTT"
	HighStdDevRtt types.Reason = "[icmp check] high RTT standard deviation"
)

func NewPinger(ip string) *Pinger {
	return &Pinger{
		IP:            ip,
		PauseActive:   false,
		PauseDuration: 0,
		PauseSince:    time.Time{},
	}
}

func (p *Pinger) ping(ctx context.Context) (*probing.Statistics, error) {
	pinger, err := probing.NewPinger(p.IP)
	pinger.SetPrivileged(true)
	if err != nil {
		return nil, fmt.Errorf("create pinger: %w", err)
	}
	pinger.Count = 28
	pinger.Interval = time.Second * 2
	pinger.Timeout = time.Second * 58
	err = pinger.RunWithContext(ctx) // Blocks until finished.
	if err != nil {
		return nil, fmt.Errorf("run ping: %w", err)
	}
	return pinger.Statistics(), nil
}

func (p *Pinger) checkHealth(stats *probing.Statistics) (bool, types.Reason) {
	if stats.PacketLoss > 20.0 {
		return false, PacketLoss
	}
	if stats.AvgRtt > (time.Millisecond * 200) {
		return false, HighAvgRtt
	}
	if stats.MaxRtt > (time.Millisecond * 800) {
		return false, HighMaxRtt
	}
	if stats.MinRtt > (time.Millisecond * 100) {
		return false, HighMinRtt
	}
	if stats.StdDevRtt > (time.Millisecond * 80) {
		return false, HighStdDevRtt
	}
	return true, ""
}

func (p *Pinger) Run(
	ctx context.Context,
	out chan<- types.Result,
) {
	for {
		select {
		case <-ctx.Done():
			return

		default:
			alive := false
			reason := types.Reason("")
			stats, err := p.ping(ctx)
			if err == nil {
				alive, reason = p.checkHealth(stats)
			} else {
				time.Sleep(time.Second * 58)
			}

			select {
			case out <- types.Result{
				IP:        p.IP,
				Timestamp: time.Now(),
				Alive:     alive,
				Err:       err,
				Reason:    reason,
				Checker:   "ping",
			}:
			case <-ctx.Done():
				return
			}
		}
	}
}
