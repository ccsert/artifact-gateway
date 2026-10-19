package config

import (
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strings"
)

// A source administrator cannot modify this operator-owned network policy.
func parseMigrationSourcePrivateEndpoints(raw string) (map[string][]netip.Prefix, error) {
	out := map[string][]netip.Prefix{}
	hostPattern := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	for _, entry := range splitCSV(raw) {
		host, cidr, ok := strings.Cut(entry, "=")
		host = strings.ToLower(strings.TrimSpace(host))
		cidr = strings.TrimSpace(cidr)
		prefix, err := netip.ParsePrefix(cidr)
		if !ok || err != nil || (!hostPattern.MatchString(host) && net.ParseIP(host) == nil) || !privateMigrationPrefix(prefix) {
			return nil, errors.New("GATEWAY_MIGRATION_SOURCE_PRIVATE_ENDPOINTS requires exact host=private-CIDR entries")
		}
		out[host] = append(out[host], prefix.Masked())
	}
	return out, nil
}

func privateMigrationPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() {
		return false
	}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
		allowed := netip.MustParsePrefix(cidr)
		if prefix.Bits() >= allowed.Bits() && allowed.Contains(prefix.Masked().Addr()) {
			return true
		}
	}
	return false
}
