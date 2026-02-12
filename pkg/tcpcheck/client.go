package tcpcheck

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"time"

	"github.com/identw/bird-rtt-keeper/pkg/types"
)

type TcpChecker struct {
	IP            string
	Port          string
	PauseActive   bool
	PauseDuration time.Duration
	PauseSince    time.Time
}

type testResult struct {
	ip         string
	attempt    int
	success    bool
	duration   time.Duration
	bytesSent  uint32
	throughput float64
	err        error
}

type aggResult struct {
	percentErr     float64
	minDuration    time.Duration
	maxDuration    time.Duration
	avgDuration    time.Duration
	stdDevDuration time.Duration
}

const (
	HighErrorPercent types.Reason = "high error percentage"
	LowAvgSpeed      types.Reason = "low average speed"
	LowMaxSpeed      types.Reason = "low maximum speed"
	LowMinSpeed      types.Reason = "low minimum speed"
	HighStdDevSpeed  types.Reason = "high speed standard deviation"

	MaxPercentErr     = 20.0
	MaxAvgDuration    = time.Second * 10
	MaxDuration       = time.Second * 18
	MinDuration       = time.Second * 12
	MaxStdDevDuration = time.Second * 7

	timeoutDefaultStr = "20s"
	sizeDefault       = "10MB"
	repeatDefault     = 1
)

func NewTcpChecker(ip string) *TcpChecker {
	return &TcpChecker{
		IP:            ip,
		Port:          DefaultPortStr,
		PauseActive:   false,
		PauseDuration: 0,
		PauseSince:    time.Time{},
	}
}

func (t *TcpChecker) tcpCheck(ctx context.Context, operation string) ([]testResult, error) {

	timeout, err := time.ParseDuration(timeoutDefaultStr)
	if err != nil {
		log.Printf("Invalid timeout format '%s': %v\n", timeoutDefaultStr, err)
		return []testResult{{
			ip:      t.IP,
			success: false,
			err:     fmt.Errorf("invalid timeout format: %w", err),
		}}, nil
	}
	dataSize, err := parseSize(sizeDefault)
	if err != nil {
		log.Printf("Invalid size format '%s': %v\n", sizeDefault, err)
		return []testResult{{
			ip:      t.IP,
			success: false,
			err:     err,
		}}, nil
	}

	var results []testResult

	for i := 1; i <= repeatDefault; i++ {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}
		dialer := &net.Dialer{
			Timeout: timeout,
		}
		conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%s", t.IP, t.Port))
		if err != nil {
			results = append(results, testResult{
				ip:      t.IP,
				attempt: i,
				success: false,
				err:     err,
			})
			continue
		}

		// close connection on context cancellation
		go func() {
			<-ctx.Done()
			conn.Close()
		}()

		// Set deadline for the entire operation
		conn.SetDeadline(time.Now().Add(timeout))

		var testErr error
		var duration time.Duration

		switch operation {
		case "download":
			duration, testErr = performDownload(conn, dataSize)
		case "upload":
			duration, testErr = performUpload(conn, dataSize)
		default:
			testErr = fmt.Errorf("unknown operation: %s", operation)
		}

		conn.Close()
		throughput := float64(dataSize) / duration.Seconds() / 1024 / 1024

		result := testResult{
			ip:         t.IP,
			attempt:    i,
			success:    testErr == nil,
			duration:   duration,
			bytesSent:  dataSize,
			throughput: throughput,
			err:        testErr,
		}

		results = append(results, result)

		if i < repeatDefault {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return results, nil
}

func (t *TcpChecker) checkHealth(stats []testResult) (bool, types.Reason) {
	r := aggResult{}
	for _, s := range stats {
		if s.err != nil {
			r.percentErr += 100.0 / float64(len(stats))
		}
		if s.duration > 0 {
			r.avgDuration += s.duration / time.Duration(len(stats))
			if r.minDuration == 0 || s.duration < r.minDuration {
				r.minDuration = s.duration
			}
			if s.duration > r.maxDuration {
				r.maxDuration = s.duration
			}
			// Calculate standard deviation
			diff := s.duration - r.avgDuration
			r.stdDevDuration += diff * diff / time.Duration(len(stats))
		}
	}

	if r.percentErr > 20.0 {
		return false, HighErrorPercent
	}
	if r.avgDuration > MaxAvgDuration {
		return false, LowAvgSpeed
	}
	if r.maxDuration > MaxDuration {
		return false, LowMaxSpeed
	}
	if r.minDuration > (time.Second * 5) {
		return false, LowMinSpeed
	}
	if r.stdDevDuration > (time.Second * 5) {
		return false, HighStdDevSpeed
	}
	return true, ""
}

func (t *TcpChecker) Run(
	ctx context.Context,
	out chan<- types.Result,
) {
	jitter := time.Duration(30+rand.Intn(120)) * time.Second
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	for {
		select {
		case <-ctx.Done():
			return

		default:
			alive := false
			reason := types.Reason("")
			downloadStats, err1 := t.tcpCheck(ctx, "download")
			uploadStats, err2 := t.tcpCheck(ctx, "upload")

			if err1 == nil && err2 == nil {
				alive, reason = t.checkHealth(append(downloadStats, uploadStats...))
			}

			var err error
			if err1 != nil && err2 != nil {
				err = fmt.Errorf("download error: %v; upload error: %v", err1, err2)
			} else if err1 != nil {
				err = fmt.Errorf("download error: %v", err1)
			} else if err2 != nil {
				err = fmt.Errorf("upload error: %v", err2)
			}

			select {
			case out <- types.Result{
				IP:        t.IP,
				Timestamp: time.Now(),
				Alive:     alive,
				Err:       err,
				Reason:    reason,
				Checker:   "tcpcheck",
			}:
			case <-ctx.Done():
				return
			}

			jitter = time.Duration(30+rand.Intn(180)) * time.Second
			timer := time.NewTimer(5*time.Minute + jitter)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}

		}
	}
}
