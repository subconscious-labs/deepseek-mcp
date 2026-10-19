package safety

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

const maxURLLength = 2000

var forbiddenNets = mustCIDRs([]string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
	"169.254.0.0/16", "fe80::/10",
	"100.64.0.0/10",
	"0.0.0.0/8", "240.0.0.0/4",
	"224.0.0.0/4", "ff00::/8",
	"::/128",
})

func mustCIDRs(ss []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(ss))
	for _, s := range ss {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("bad cidr: " + s)
		}
		out = append(out, n)
	}
	return out
}

// ValidateURL нормализует URL и проверяет его на SSRF.
// resolveDNS=false используется для ссылок из search-результатов:
// проверяются схема, порт и IP-литералы, но без DNS-резолва.
func ValidateURL(raw string, allowPrivate, resolveDNS bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty url")
	}
	if len(raw) > maxURLLength {
		return "", fmt.Errorf("url exceeds %d characters", maxURLLength)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Scheme = scheme

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if len(host) > 253 {
		return "", fmt.Errorf("host too long")
	}

	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return "", fmt.Errorf("port %s not allowed", port)
	}

	u.User = nil
	u.Fragment = ""

	if allowPrivate {
		return u.String(), nil
	}

	if ip := net.ParseIP(host); ip != nil {
		if isForbiddenIP(ip) {
			return "", fmt.Errorf("address %s is not allowed", ip)
		}
		return u.String(), nil
	}

	// Блокируем очевидные localhost-имена без DNS.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return "", fmt.Errorf("host %q is not allowed", host)
	}

	if !resolveDNS {
		return u.String(), nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("dns lookup %q: %w", host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no addresses for %q", host)
	}
	for _, ip := range ips {
		if isForbiddenIP(ip) {
			return "", fmt.Errorf("host %q resolves to forbidden address %s", host, ip)
		}
	}
	return u.String(), nil
}

func isForbiddenIP(ip net.IP) bool {
	for _, n := range forbiddenNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
