package metrics

import "strings"

// VPNFromPeer extracts the VPN identifier from a BGP peer protocol name.
//
// If the suffix after the last underscore is a short alphabetic code (e.g. vpn501-1_oc -> oc),
// that suffix is used. Otherwise the prefix before the first underscore is used
// (e.g. oc_91-1-501-4 -> oc).
func VPNFromPeer(peer string) string {
	if idx := strings.LastIndex(peer, "_"); idx >= 0 && idx < len(peer)-1 {
		suffix := peer[idx+1:]
		if isAlphabeticVPNCode(suffix) {
			return suffix
		}
	}
	if idx := strings.Index(peer, "_"); idx > 0 {
		return peer[:idx]
	}
	return "unknown"
}

func isAlphabeticVPNCode(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
