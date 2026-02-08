package security

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateServerBaseURL enforces MVP SSRF protections:
// - https only
// - no localhost/127.0.0.1
// - no private/loopback/link-local ranges
// Returns normalized base URL (scheme+host[:port]).
func ValidateServerBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid url: %q", raw)
	}
	if u.Scheme != "https" {
		return "", errors.New("only https is allowed")
	}

	host := u.Hostname()
	if host == "" {
		return "", errors.New("empty hostname")
	}
	if isLocalHostname(host) {
		return "", errors.New("localhost is not allowed")
	}

	ips, err := net.LookupIP(host)
	if err == nil {
		for _, ip := range ips {
			if isDisallowedIP(ip) {
				return "", fmt.Errorf("host resolves to disallowed ip: %s", ip.String())
			}
		}
	}

	normalized := url.URL{Scheme: u.Scheme, Host: u.Host}
	return normalized.String(), nil
}

func isLocalHostname(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	return h == "localhost" || h == "localhost.localdomain"
}

func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Normalize to 16-byte form where possible.
	ip = ip.To16()
	if ip == nil {
		return true
	}
	// Loopback, link-local, multicast, unspecified.
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// Private ranges (RFC1918) + unique local IPv6 (fc00::/7).
	if isPrivate(ip) {
		return true
	}
	return false
}

func isPrivate(ip net.IP) bool {
	// IPv4-mapped?
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 10:
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
			return true
		case v4[0] == 192 && v4[1] == 168:
			return true
		case v4[0] == 127:
			return true
		case v4[0] == 169 && v4[1] == 254:
			return true
		}
		return false
	}
	// IPv6 ULA fc00::/7
	return ip[0]&0xfe == 0xfc
}

