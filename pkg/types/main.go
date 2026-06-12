package types

import (
	"time"
)

type Result struct {
	IP        string
	Alive     bool
	Timestamp time.Time
	Err       error
	Reason    Reason
	Checker   string
	Icmp      *IcmpStats
	Tcp       *TcpStats
}

type Reason string

type IcmpStats struct {
	PacketLoss float64
	AvgRtt     time.Duration
	MinRtt     time.Duration
	MaxRtt     time.Duration
	StdDevRtt  time.Duration
}

type TcpStats struct {
	AvgDuration           time.Duration
	MinDuration           time.Duration
	MaxDuration           time.Duration
	ThroughputBytesPerSec float64
}
