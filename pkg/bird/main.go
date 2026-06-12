package bird

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"regexp"
	"strconv"

	"github.com/identw/bird-rtt-keeper/pkg/birdsocket"
)

var (
	neighborAddressRegex = regexp.MustCompile(`Neighbor address:\s+(\d{1,3}(?:\.\d{1,3}){3})`)
	stateRegex           = regexp.MustCompile(`State:\s+UP`)
	bgpPeerRegex         = regexp.MustCompile(`^\s+([^\s]+)\s+BGP\s.*`)
	routesRegex          = regexp.MustCompile(`Routes:\s+(\d+)\s+imported,\s+(\d+)\s+exported`)
)

type BgpPeer struct {
	Name             string
	IP               string
	State            bool
	PrefixesImported int
	PrefixesExported int
}

type BirdClient struct {
	socket *birdsocket.BirdSocket
}

func NewBirdClient(socketPath string) *BirdClient {
	return &BirdClient{
		socket: birdsocket.NewSocket(socketPath),
	}
}

func parseBgpProtocolOutput(show []byte) (BgpPeer, error) {
	match := neighborAddressRegex.FindSubmatch(show)
	if match == nil {
		return BgpPeer{}, fmt.Errorf("no neighbor address found in bird output")
	}

	peer := BgpPeer{
		IP:    string(match[1]),
		State: stateRegex.Match(show),
	}

	if routesMatch := routesRegex.FindSubmatch(show); routesMatch != nil {
		peer.PrefixesImported, _ = strconv.Atoi(string(routesMatch[1]))
		peer.PrefixesExported, _ = strconv.Atoi(string(routesMatch[2]))
	}

	return peer, nil
}

func (c *BirdClient) GetBgpProtocol(peer string) (BgpPeer, error) {
	if _, err := c.socket.Connect(); err != nil {
		return BgpPeer{}, fmt.Errorf("connect bird socket: %w", err)
	}
	defer c.socket.Close()

	show, err := c.socket.Query("show protocols all " + peer)
	if err != nil {
		return BgpPeer{}, fmt.Errorf("query bird socket: %w", err)
	}

	details, err := parseBgpProtocolOutput(show)
	if err != nil {
		return BgpPeer{}, err
	}
	details.Name = peer
	return details, nil
}

func (c *BirdClient) GetProtocols() ([]string, error) {
	if _, err := c.socket.Connect(); err != nil {
		return nil, fmt.Errorf("connect bird socket: %w", err)
	}
	defer c.socket.Close()

	show, err := c.socket.Query("show protocols")
	if err != nil {
		return nil, fmt.Errorf("query bird socket: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(show))
	peers := make([]string, 0)

	for scanner.Scan() {
		match := bgpPeerRegex.FindSubmatch(scanner.Bytes())
		if len(match) > 1 {
			peers = append(peers, string(match[1]))
		}
	}

	if len(peers) == 0 {
		return nil, fmt.Errorf("no BGP peers found in bird output")
	}

	return peers, nil
}

func (c *BirdClient) DisableProtocol(peer string) error {
	if _, err := c.socket.Connect(); err != nil {
		return fmt.Errorf("connect bird socket: %w", err)
	}
	defer c.socket.Close()

	_, err := c.socket.Query("disable " + peer)
	if err != nil {
		return fmt.Errorf("disable %s: %w", peer, err)
	}

	return nil
}

func (c *BirdClient) EnableProtocol(peer string) error {
	if _, err := c.socket.Connect(); err != nil {
		return fmt.Errorf("connect bird socket: %w", err)
	}
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
		details, err := c.GetBgpProtocol(peer)
		if err != nil {
			log.Printf("Error getting BGP protocol for %s: %v", peer, err)
			continue
		}
		bgpPeers = append(bgpPeers, details)
	}
	return bgpPeers, nil
}
