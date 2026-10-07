package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type ociBearerToken struct {
	value   string
	expires time.Time
	version uint64
}
type ociBearerFlight struct {
	done  chan struct{}
	token ociBearerToken
	err   error
}
type ociBearerEntry struct {
	token ociBearerToken
	used  uint64
}

// OCIBearerTokenCache is bounded, process-local and never stores refresh tokens.
type OCIBearerTokenCache struct {
	mu       sync.Mutex
	entries  map[[32]byte]ociBearerEntry
	flights  map[[32]byte]*ociBearerFlight
	sequence uint64
}

func NewOCIBearerTokenCache() *OCIBearerTokenCache {
	return &OCIBearerTokenCache{entries: make(map[[32]byte]ociBearerEntry), flights: make(map[[32]byte]*ociBearerFlight)}
}
func (c UpstreamClient) ociBearerCacheKey(member repository.Member, scope string) ([32]byte, error) {
	identity := member.RepositoryID
	if identity == "" {
		identity = member.Name
	}
	effective := []string{fmt.Sprintf("%p/%p", c.HTTPClient, c.EgressHooks)}
	if member.EgressProxy == nil || member.EgressProxy.Mode == "" || member.EgressProxy.Mode == repository.EgressProxyModeEnvironment {
		for _, target := range []string{member.Endpoint, member.OCIBearer.Realm} {
			request, err := http.NewRequest(http.MethodGet, target, nil)
			if err != nil {
				return [32]byte{}, err
			}
			proxy, err := c.upstreamEgressHooks().ProxyFromEnvironment(request)
			if err != nil {
				return [32]byte{}, err
			}
			value := "direct"
			if proxy != nil {
				value = proxy.String()
			}
			effective = append(effective, value)
		}
	}
	data, _ := json.Marshal([]any{identity, member.Endpoint, member.OCIBearer.Realm, member.OCIBearer.Service, scope, member.AllowedHosts, member.EgressProxy, effective})
	return sha256.Sum256(data), nil
}
func (c *OCIBearerTokenCache) lookup(key [32]byte) ociBearerToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return ociBearerToken{}
	}
	if !time.Now().Add(5 * time.Second).Before(entry.token.expires) {
		delete(c.entries, key)
		return ociBearerToken{}
	}
	c.sequence++
	entry.used = c.sequence
	c.entries[key] = entry
	return entry.token
}
func (c *OCIBearerTokenCache) invalidate(key [32]byte, version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && entry.token.version == version {
		delete(c.entries, key)
	}
}
func (c *OCIBearerTokenCache) get(ctx context.Context, key [32]byte, refresh func(context.Context) (ociBearerToken, error)) (ociBearerToken, error) {
	if token := c.lookup(key); token.value != "" {
		return token, nil
	}
	c.mu.Lock()
	// Recheck under lock to avoid starting a second refresh after a completed flight.
	if entry, ok := c.entries[key]; ok && time.Now().Add(5*time.Second).Before(entry.token.expires) {
		c.mu.Unlock()
		return entry.token, nil
	}
	flight := c.flights[key]
	if flight == nil {
		if len(c.flights) >= 32 {
			c.mu.Unlock()
			return ociBearerToken{}, errOCIBearerExchange
		}
		flight = &ociBearerFlight{done: make(chan struct{})}
		c.flights[key] = flight
		go func() {
			work, cancel := context.WithTimeout(context.WithoutCancel(ctx), ociSharedWorkTimeout)
			defer cancel()
			token, err := refresh(work)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err == nil {
				c.sequence++
				token.version = c.sequence
				for k, e := range c.entries {
					if !time.Now().Before(e.token.expires) {
						delete(c.entries, k)
					}
				}
				if len(c.entries) >= 256 {
					var oldest [32]byte
					var used uint64 = ^uint64(0)
					for k, e := range c.entries {
						if e.used < used {
							oldest = k
							used = e.used
						}
					}
					delete(c.entries, oldest)
				}
				c.entries[key] = ociBearerEntry{token: token, used: c.sequence}
			}
			flight.token, flight.err = token, err
			delete(c.flights, key)
			close(flight.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ociBearerToken{}, ctx.Err()
	case <-flight.done:
		return flight.token, flight.err
	}
}
