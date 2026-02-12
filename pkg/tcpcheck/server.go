package tcpcheck

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

var (
	DefaultPortStr = "32486"
	defaultPorts   = []int{32486}
)

func Run(ports []int) {
	// Get list of ports (priority: flag -> environment variable -> default values)
	var wg sync.WaitGroup

	// Start server on each port in a separate goroutine
	for _, port := range ports {
		wg.Add(1)
		go startServer(port, &wg)
	}

	log.Printf("Servers started on ports: %v\n", ports)
	log.Println("Press Ctrl+C to stop")

	// Wait for all servers to finish (will never finish without signal)
	wg.Wait()
}

// handleConnection handles an incoming TCP connection
func handleConnection(conn net.Conn) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	log.Printf("New connection from %s\n", clientAddr)

	reader := bufio.NewReader(conn)

	// Read message type
	msgType, err := reader.ReadByte()
	if err != nil {
		log.Printf("Error reading message type from %s: %v\n", clientAddr, err)
		return
	}

	switch msgType {
	case MessageTypeDownload:
		handleDownload(conn, reader, clientAddr)
	case MessageTypeUpload:
		handleUpload(conn, reader, clientAddr)
	default:
		log.Printf("Unknown message type %d from %s\n", msgType, clientAddr)
	}

	log.Printf("Connection closed: %s\n", clientAddr)
}

// handleDownload generates random data and sends it with hash
func handleDownload(conn net.Conn, reader *bufio.Reader, clientAddr string) {
	// Read requested size (4 bytes, big endian)
	var size uint32
	err := binary.Read(reader, binary.BigEndian, &size)
	if err != nil {
		log.Printf("Error reading size from %s: %v\n", clientAddr, err)
		return
	}

	if size > MaxDataSize {
		log.Printf("Requested size %d exceeds maximum %d from %s\n", size, MaxDataSize, clientAddr)
		return
	}

	log.Printf("Download request from %s: %d bytes\n", clientAddr, size)

	// Generate random data
	data := make([]byte, size)
	rand.Read(data)

	// Calculate hash
	hash := sha256.Sum256(data)

	// Send response: size + data + hash
	err = binary.Write(conn, binary.BigEndian, size)
	if err != nil {
		log.Printf("Error writing size to %s: %v\n", clientAddr, err)
		return
	}

	n, err := conn.Write(data)
	if err != nil {
		log.Printf("Error writing data to %s: %v\n", clientAddr, err)
		return
	}
	if n != int(size) {
		log.Printf("Error writing data to %s: wrote %d bytes instead of %d\n", clientAddr, n, size)
		return
	}

	n, err = conn.Write(hash[:])
	if err != nil {
		log.Printf("Error writing hash to %s: %v\n", clientAddr, err)
		return
	}
	if n != 32 {
		log.Printf("Error writing hash to %s: wrote %d bytes instead of 32\n", clientAddr, n)
		return
	}

	log.Printf("Sent %d bytes to %s\n", size, clientAddr)
}

// handleUpload receives data and validates hash
func handleUpload(conn net.Conn, reader *bufio.Reader, clientAddr string) {
	// Read data size (4 bytes, big endian)
	var size uint32
	err := binary.Read(reader, binary.BigEndian, &size)
	if err != nil {
		log.Printf("Error reading size from %s: %v\n", clientAddr, err)
		return
	}

	if size > MaxDataSize {
		log.Printf("Upload size %d exceeds maximum %d from %s\n", size, MaxDataSize, clientAddr)
		conn.Write([]byte{0}) // Send failure
		return
	}

	log.Printf("Upload request from %s: %d bytes\n", clientAddr, size)

	// Read data
	data := make([]byte, size)
	_, err = io.ReadFull(reader, data)
	if err != nil {
		log.Printf("Error reading data from %s: %v\n", clientAddr, err)
		return
	}

	// Read hash (32 bytes)
	var receivedHash [32]byte
	_, err = io.ReadFull(reader, receivedHash[:])
	if err != nil {
		log.Printf("Error reading hash from %s: %v\n", clientAddr, err)
		return
	}

	// Calculate hash of received data
	calculatedHash := sha256.Sum256(data)

	// Compare hashes
	var result byte
	if calculatedHash == receivedHash {
		result = 1
		log.Printf("Upload from %s validated successfully\n", clientAddr)
	} else {
		result = 0
		log.Printf("Upload from %s failed validation\n", clientAddr)
	}

	// Send result
	n, err := conn.Write([]byte{result})
	if err != nil {
		log.Printf("Error writing result to %s: %v\n", clientAddr, err)
		return
	}
	if n != 1 {
		log.Printf("Error writing result to %s: wrote %d bytes instead of 1\n", clientAddr, n)
	}
}

// startServer starts a TCP server on the specified port
func startServer(port int, wg *sync.WaitGroup) {
	defer wg.Done()

	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Printf("Failed to start server on port %d: %v\n", port, err)
		return
	}
	defer listener.Close()

	log.Printf("Server started on port %d\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection on port %d: %v\n", port, err)
			continue
		}

		// Handle each connection in a separate goroutine
		go handleConnection(conn)
	}
}

// getPorts retrieves the list of ports from flag or environment variable
func GetPorts(portsStr string) []int {
	// If flag is not set, try to read from environment variable
	if portsStr == "" {
		portsStr = os.Getenv("PORTS")
	}

	// If nothing is set, use default ports
	if portsStr == "" {
		return defaultPorts
	}

	// Parse ports string
	parts := strings.Split(portsStr, ",")
	ports := make([]int, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		port, err := strconv.Atoi(part)
		if err != nil {
			log.Printf("Warning: invalid port '%s', skipping\n", part)
			continue
		}

		if port < 1 || port > 65535 {
			log.Printf("Warning: port %d is out of valid range (1-65535), skipping\n", port)
			continue
		}

		ports = append(ports, port)
	}

	if len(ports) == 0 {
		log.Println("No valid ports specified, using default ports")
		return defaultPorts
	}

	return ports
}
