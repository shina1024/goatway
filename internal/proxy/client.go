package proxy

import (
	"net"
	"net/http"
	"sync"
	"time"

	"goatway/internal/targetgroup"
)

type clientKey struct {
	connectTimeout      time.Duration
	readTimeout         time.Duration
	idleConnTimeout     time.Duration
	maxIdleConnsPerHost int
}

type clientCache struct {
	mu      sync.Mutex
	clients map[clientKey]*http.Client
}

func (cache *clientCache) get(target targetgroup.Target, maxIdleConnsPerHost int) *http.Client {
	key := clientKey{
		connectTimeout:      target.ConnectTimeout(),
		readTimeout:         target.ReadTimeout(),
		idleConnTimeout:     target.IdleConnTimeout(),
		maxIdleConnsPerHost: maxIdleConnsPerHost,
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if client, exists := cache.clients[key]; exists {
		return client
	}
	client := &http.Client{
		Timeout: key.readTimeout,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: key.connectTimeout}).DialContext,
			IdleConnTimeout:     key.idleConnTimeout,
			MaxIdleConnsPerHost: key.maxIdleConnsPerHost,
		},
	}
	cache.clients[key] = client
	return client
}
