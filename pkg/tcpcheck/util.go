package tcpcheck

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// parseSize parses size strings like "1KB", "512KB", "1MB", "16MB"
func parseSize(sizeStr string) (uint32, error) {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))

	multiplier := uint64(1)
	numStr := sizeStr

	if strings.HasSuffix(sizeStr, "KB") {
		multiplier = 1024
		numStr = strings.TrimSuffix(sizeStr, "KB")
	} else if strings.HasSuffix(sizeStr, "MB") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSuffix(sizeStr, "MB")
	} else if strings.HasSuffix(sizeStr, "GB") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(sizeStr, "GB")
	} else if strings.HasSuffix(sizeStr, "B") {
		multiplier = 1
		numStr = strings.TrimSuffix(sizeStr, "B")
	}

	num, err := strconv.ParseUint(numStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}

	result := num * multiplier
	if result > uint64(MaxDataSize) {
		return 0, fmt.Errorf("size exceeds maximum")
	}

	return uint32(result), nil
}

// performDownload requests data from server and validates hash
func performDownload(conn net.Conn, size uint32) (time.Duration, error) {
	start := time.Now()

	// Send download request: type + size
	n, err := conn.Write([]byte{MessageTypeDownload})
	if err != nil {
		return 0, fmt.Errorf("error writing message type: %w", err)
	}
	if n != 1 {
		return 0, fmt.Errorf("wrote %d bytes instead of 1 for message type", n)
	}

	err = binary.Write(conn, binary.BigEndian, size)
	if err != nil {
		return 0, fmt.Errorf("error writing size: %w", err)
	}

	reader := bufio.NewReader(conn)

	// Read response size
	var responseSize uint32
	err = binary.Read(reader, binary.BigEndian, &responseSize)
	if err != nil {
		return 0, fmt.Errorf("error reading response size: %w", err)
	}

	if responseSize != size {
		return 0, fmt.Errorf("unexpected response size: expected %d, got %d", size, responseSize)
	}

	// Read data
	data := make([]byte, responseSize)
	_, err = io.ReadFull(reader, data)
	if err != nil {
		return 0, fmt.Errorf("error reading data: %w", err)
	}

	// Read hash
	var receivedHash [32]byte
	_, err = io.ReadFull(reader, receivedHash[:])
	if err != nil {
		return 0, fmt.Errorf("error reading hash: %w", err)
	}

	duration := time.Since(start)

	// Validate hash
	calculatedHash := sha256.Sum256(data)
	if calculatedHash != receivedHash {
		return duration, fmt.Errorf("hash validation failed")
	}

	return duration, nil
}

// performUpload sends data to server for validation
func performUpload(conn net.Conn, size uint32) (time.Duration, error) {
	// Generate random data
	data := make([]byte, size)
	rand.Read(data)

	// Calculate hash
	hash := sha256.Sum256(data)

	start := time.Now()

	// Send upload request: type + size + data + hash
	n, err := conn.Write([]byte{MessageTypeUpload})
	if err != nil {
		return 0, fmt.Errorf("error writing message type: %w", err)
	}
	if n != 1 {
		return 0, fmt.Errorf("wrote %d bytes instead of 1 for message type", n)
	}

	err = binary.Write(conn, binary.BigEndian, size)
	if err != nil {
		return 0, fmt.Errorf("error writing size: %w", err)
	}

	n, err = conn.Write(data)
	if err != nil {
		return 0, fmt.Errorf("error writing data: %w", err)
	}
	if n != int(size) {
		return 0, fmt.Errorf("wrote %d bytes instead of %d for data", n, size)
	}

	n, err = conn.Write(hash[:])
	if err != nil {
		return 0, fmt.Errorf("error writing hash: %w", err)
	}
	if n != 32 {
		return 0, fmt.Errorf("wrote %d bytes instead of 32 for hash", n)
	}

	// Read result
	result := make([]byte, 1)
	_, err = io.ReadFull(conn, result)
	if err != nil {
		return 0, fmt.Errorf("error reading result: %w", err)
	}

	duration := time.Since(start)

	if result[0] != 1 {
		return duration, fmt.Errorf("server validation failed")
	}

	return duration, nil
}
