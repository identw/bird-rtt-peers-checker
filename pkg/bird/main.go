package bird

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"regexp"

	"github.com/identw/bird-rtt-keeper/pkg/birdsocket"
)

var neighborAddressRegex *regexp.Regexp
var bgpPeerRegex *regexp.Regexp
var stateRegex *regexp.Regexp

type BgpPeer struct {
	Name  string
	IP    string
	State bool
}

func init() {
	neighborAddressRegex = regexp.MustCompile(`Neighbor address: (\d{1,3}(?:\.\d{1,3}){3})`)
	stateRegex = regexp.MustCompile(`State:\s+UP`)
	bgpPeerRegex = regexp.MustCompile(`^\s+([^\s]+)\s+BGP\s.*`)
}

type BirdClient struct {
	socket *birdsocket.BirdSocket
}

func NewBirdClient(socketPath string) *BirdClient {
	return &BirdClient{
		socket: birdsocket.NewSocket(socketPath),
	}
}

func (c *BirdClient) GetBgpProtocol(peer string) (string, bool, error) {
	c.socket.Connect()
	defer c.socket.Close()

	show, err := c.socket.Query("show protocols all " + peer)
	if err != nil {
		return "", false, fmt.Errorf("query bird socket: %w", err)
	}
	match := neighborAddressRegex.FindSubmatch(show)

	if match == nil {
		return "", false, fmt.Errorf("no neighbor address found in bird output")
	}
	ip := string(match[1])

	stateMatch := stateRegex.Match(show)

	return ip, stateMatch, nil
}

func (c *BirdClient) GetProtocols() ([]string, error) {
	c.socket.Connect()
	defer c.socket.Close()

	show, err := c.socket.Query("show protocols")
	if err != nil {
		return nil, fmt.Errorf("query bird socket: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(show))
	peers := make([]string, 0)

	for scanner.Scan() {
		match := bgpPeerRegex.FindSubmatch(scanner.Bytes())
		if match != nil && len(match) > 1 {
			peers = append(peers, string(match[1]))
		}
	}

	if len(peers) == 0 {
		return nil, fmt.Errorf("no BGP peers found in bird output")
	}

	return peers, nil
}

func (c *BirdClient) DisableProtocol(peer string) error {
	c.socket.Connect()
	defer c.socket.Close()

	_, err := c.socket.Query("disable " + peer)
	if err != nil {
		return fmt.Errorf("disable %s: %w", peer, err)
	}

	return nil
}

func (c *BirdClient) EnableProtocol(peer string) error {
	c.socket.Connect()
	defer c.socket.Close()

	_, err := c.socket.Query("enable " + peer)
	if err != nil {
		return fmt.Errorf("enable %s: %w", peer, err)
	}

	return nil
}

func (c *BirdClient) ReadBgpPeers() ([]BgpPeer, error) {

	peers, err := c.GetProtocols()
	if err != nil {
		return nil, fmt.Errorf("get protocols: %w", err)
	}

	bgpPeers := make([]BgpPeer, 0, len(peers))
	for _, peer := range peers {
		ip, state, err := c.GetBgpProtocol(string(peer))
		if err != nil {
			log.Printf("Error getting BGP protocol for %s: %v", peer, err)
			continue
		}
		bgpPeers = append(bgpPeers, BgpPeer{
			Name:  string(peer),
			IP:    ip,
			State: state,
		})
	}
	return bgpPeers, nil
}
