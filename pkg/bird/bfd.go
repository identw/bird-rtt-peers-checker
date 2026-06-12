package bird

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var bfdSessionLineRegex = regexp.MustCompile(`^\s*(\d{1,3}(?:\.\d{1,3}){3})\s+\S+\s+(\S+)\s+\S+\s+([\d.]+)\s+([\d.]+)\s*$`)

// BfdSession describes a BFD session reported by bird.
type BfdSession struct {
	IP       string
	State    bool // true when State is "Up"
	Interval float64
	Timeout  float64
}

// ReadBfdSessions returns BFD sessions from "show bfd sessions".
// Returns an empty slice when no BFD protocol is running.
func (c *BirdClient) ReadBfdSessions() ([]BfdSession, error) {
	c.socket.Connect()
	defer c.socket.Close()

	show, err := c.socket.Query("show bfd sessions")
	if err != nil {
		return nil, fmt.Errorf("query bfd sessions: %w", err)
	}

	return parseBfdSessionsOutput(show), nil
}

func parseBfdSessionsOutput(show []byte) []BfdSession {
	output := string(show)
	if strings.Contains(output, "There is no BFD protocol running") {
		return nil
	}

	sessions := make([]BfdSession, 0)
	scanner := bufio.NewScanner(bytes.NewReader(show))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "IP address") {
			continue
		}

		match := bfdSessionLineRegex.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		interval, _ := strconv.ParseFloat(match[3], 64)
		timeout, _ := strconv.ParseFloat(match[4], 64)

		sessions = append(sessions, BfdSession{
			IP:       match[1],
			State:    strings.EqualFold(match[2], "Up"),
			Interval: interval,
			Timeout:  timeout,
		})
	}

	return sessions
}

// BfdSessionsByIP indexes BFD sessions by neighbor IP.
func BfdSessionsByIP(sessions []BfdSession) map[string]BfdSession {
	out := make(map[string]BfdSession, len(sessions))
	for _, s := range sessions {
		out[s.IP] = s
	}
	return out
}
