package sandbox

import (
	"fmt"
	"net/netip"
	neturl "net/url"
	"strconv"
	"strings"
)

// maxImageRedirects is the maximum number of redirects followed when
// downloading remote images.
const maxImageRedirects = 10

// sensitiveAddrPrefixes are IP ranges that are never reachable via URL
// downloads unless the allow list explicitly permits them: loopback,
// link-local (including the cloud metadata address 169.254.169.254), and
// CGNAT space.
var sensitiveAddrPrefixes = []netip.Prefix{
	mustParsePrefix("127.0.0.0/8"),
	mustParsePrefix("::1/128"),
	mustParsePrefix("169.254.0.0/16"),
	mustParsePrefix("fe80::/10"),
	mustParsePrefix("100.64.0.0/10"),
}

func mustParsePrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic(fmt.Sprintf("invalid built-in prefix %q: %v", s, err))
	}
	return p
}

func validateNetworkURLAccess(exec *Executor, rawURL string) (*neturl.URL, error) {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("url must be a valid HTTP or HTTPS URL")
	}

	cfg := execConfigOrDefault(exec)
	if err := checkNetworkHostPolicy(cfg, parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// checkNetworkHostPolicy enforces the network allow-list on a single URL.
// It is applied to the initial URL and to every redirect hop, so an
// allowed host cannot bypass the policy by redirecting to an internal
// address.
func checkNetworkHostPolicy(cfg *Config, u *neturl.URL) error {
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	port := u.Port()

	if addr, err := netip.ParseAddr(host); err == nil {
		// IP literals always require an explicit IP/CIDR allow entry; this
		// blocks loopback, link-local, and CGNAT targets (including cloud
		// metadata endpoints) regardless of the hostname they may alias.
		if !ipLiteralAllowed(cfg.Sandbox.Network.Allow, addr, port) {
			return fmt.Errorf("ip address %q is not allowed by sandbox policy", host)
		}
		return nil
	}

	if !cfg.Sandbox.Network.Isolation {
		return nil
	}
	if len(cfg.Sandbox.Network.Allow) == 0 {
		return fmt.Errorf("network access is disabled by sandbox policy")
	}
	if !hostAllowedByRules(cfg.Sandbox.Network.Allow, host, port) {
		return fmt.Errorf("host %q is not allowed by sandbox policy", host)
	}
	return nil
}

func execConfigOrDefault(exec *Executor) *Config {
	if exec != nil && exec.config != nil {
		return exec.config
	}
	return DefaultConfig()
}

// hostAllowedByRules reports whether host:port is covered by an allow entry.
// Entries without an explicit port only cover the default http/https ports.
// No DNS resolution is performed here: hostname rules match hostnames and
// IP/CIDR rules match IP literals.
func hostAllowedByRules(rules []string, host, port string) bool {
	for _, rule := range rules {
		ruleHost, rulePort := splitHostPortRule(rule)
		if !ruleHostMatches(ruleHost, host) {
			continue
		}
		if portAllowedByRule(rulePort, port) {
			return true
		}
	}
	return false
}

// ipLiteralAllowed reports whether an IP literal is covered by an explicit
// IP or CIDR allow entry.
func ipLiteralAllowed(rules []string, addr netip.Addr, port string) bool {
	for _, rule := range rules {
		ruleHost, rulePort := splitHostPortRule(rule)
		if ruleHost == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(ruleHost); err == nil {
			if prefix.Contains(addr) && portAllowedByRule(rulePort, port) {
				return true
			}
			continue
		}
		if ruleAddr, err := netip.ParseAddr(ruleHost); err == nil && ruleAddr == addr && portAllowedByRule(rulePort, port) {
			return true
		}
	}
	return false
}

func portAllowedByRule(rulePort, urlPort string) bool {
	if rulePort != "" {
		return urlPort == rulePort
	}
	// Without an explicit "host:port" entry only the default ports apply.
	return urlPort == "" || urlPort == "80" || urlPort == "443"
}

// splitHostPortRule splits an allow entry of the form "host:port" into its
// host and port parts. Bracketed IPv6 literals ("[::1]:8080") are handled.
func splitHostPortRule(rule string) (host, port string) {
	rule = strings.ToLower(strings.TrimSpace(rule))
	if strings.HasPrefix(rule, "[") {
		end := strings.LastIndex(rule, "]")
		if end < 0 {
			return rule, ""
		}
		rest := rule[end+1:]
		if strings.HasPrefix(rest, ":") {
			return rule[1:end], rest[1:]
		}
		return rule[1:end], ""
	}
	// A single colon separates host and port; more colons means a bare IPv6
	// literal without a port.
	if i := strings.LastIndex(rule, ":"); i >= 0 && strings.Count(rule, ":") == 1 {
		return rule[:i], rule[i+1:]
	}
	return rule, ""
}

func ruleHostMatches(rule, host string) bool {
	rule = strings.ToLower(strings.TrimSpace(rule))
	host = strings.ToLower(strings.TrimSpace(host))
	if rule == "" || host == "" {
		return false
	}
	if strings.HasPrefix(rule, "*.") {
		suffix := strings.TrimPrefix(rule, "*.")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	return rule == host
}

// validateNetworkAllowEntry rejects malformed network allow entries at config
// load time so typos cannot silently widen (or narrow) the policy.
func validateNetworkAllowEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("empty entry")
	}
	if strings.ContainsAny(entry, " \t") {
		return fmt.Errorf("entry %q must not contain whitespace", entry)
	}
	if strings.Contains(entry, "://") {
		return fmt.Errorf("entry %q must be a host, IP, or CIDR without scheme", entry)
	}
	if strings.Contains(entry, "/") {
		if _, err := netip.ParsePrefix(entry); err != nil {
			return fmt.Errorf("entry %q is not a valid CIDR: %w", entry, err)
		}
		return nil
	}

	host, port := splitHostPortRule(entry)
	if port != "" {
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("entry %q has an invalid port %q", entry, port)
		}
	}
	if host == "" {
		return fmt.Errorf("entry %q has an empty host", entry)
	}
	// Entries that look like bare IP addresses must parse as such.
	if strings.Contains(host, ":") || isDottedDecimalHost(host) {
		if _, err := netip.ParseAddr(host); err != nil {
			return fmt.Errorf("entry %q is not a valid IP address: %w", entry, err)
		}
		return nil
	}
	if strings.HasPrefix(host, "*.") {
		host = strings.TrimPrefix(host, "*.")
	}
	if host == "" {
		return fmt.Errorf("entry %q has an empty host", entry)
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return fmt.Errorf("entry %q contains invalid host character %q", entry, string(r))
		}
	}
	return nil
}

// isDottedDecimalHost reports whether host consists solely of digits and
// dots, i.e. it was probably meant to be an IPv4 address.
func isDottedDecimalHost(host string) bool {
	if !strings.Contains(host, ".") {
		return false
	}
	for _, r := range host {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
