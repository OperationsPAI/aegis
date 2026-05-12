package gateway

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ProxyPool memoizes one httputil.ReverseProxy per upstream so each
// route reuses a connection pool. Built lazily on first match.
type ProxyPool struct {
	mu      sync.RWMutex
	proxies map[string]*httputil.ReverseProxy
}

// NewProxyPool returns an empty pool.
func NewProxyPool() *ProxyPool {
	return &ProxyPool{proxies: make(map[string]*httputil.ReverseProxy)}
}

// For returns (and lazily creates) the proxy bound to the given
// upstream host. The upstream string may be `host:port` or a full URL;
// missing scheme defaults to http.
func (p *ProxyPool) For(upstream string, timeout time.Duration) (*httputil.ReverseProxy, error) {
	p.mu.RLock()
	if rp, ok := p.proxies[upstream]; ok {
		p.mu.RUnlock()
		return rp, nil
	}
	p.mu.RUnlock()

	target := upstream
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(u)
	// Override Director so we control header rewrites that
	// httputil.NewSingleHostReverseProxy would otherwise stomp on.
	defaultDirector := rp.Director
	rp.Director = func(req *http.Request) {
		defaultDirector(req)
		req.Host = u.Host
		req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", u.Scheme)
		}
	}
	rp.Transport = &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logrus.WithError(err).WithFields(logrus.Fields{
			"path":     r.URL.Path,
			"upstream": upstream,
		}).Warn("gateway: upstream error")
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.proxies[upstream]; ok {
		return existing, nil
	}
	p.proxies[upstream] = rp
	return rp, nil
}
