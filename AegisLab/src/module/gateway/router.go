package gateway

import (
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// RouteTable is an ordered set of routes; Match returns the first
// route whose prefix is a prefix of the request path. Routes are
// sorted by descending prefix length at load time so longer-prefix
// rules win even when the config order is sloppy.
type RouteTable struct {
	routes []Route
}

// NewRouteTable normalizes routes and sorts them by descending prefix
// length for first-match semantics consistent with the RFC.
func NewRouteTable(routes []Route) *RouteTable {
	cp := make([]Route, 0, len(routes))
	for _, r := range routes {
		if r.Prefix == "" || r.Upstream == "" {
			logrus.WithFields(logrus.Fields{
				"prefix":   r.Prefix,
				"upstream": r.Upstream,
			}).Warn("gateway: skipping incomplete route")
			continue
		}
		if r.Auth == "" {
			r.Auth = AuthJWT
		}
		cp = append(cp, r)
	}
	sort.SliceStable(cp, func(i, j int) bool {
		return len(cp[i].Prefix) > len(cp[j].Prefix)
	})
	return &RouteTable{routes: cp}
}

// Match returns the first route whose prefix is a path-prefix of p, or
// nil if nothing matches.
func (t *RouteTable) Match(p string) *Route {
	for i := range t.routes {
		if strings.HasPrefix(p, t.routes[i].Prefix) {
			return &t.routes[i]
		}
	}
	return nil
}

// Routes returns a snapshot copy of the underlying route slice. Useful
// for /admin/routes introspection (not wired in v1).
func (t *RouteTable) Routes() []Route {
	out := make([]Route, len(t.routes))
	copy(out, t.routes)
	return out
}

// LoadConfig reads the [gateway] section from viper. Returns a Config
// with safe defaults if the section is missing — the gateway can still
// boot, the route table will just be empty and every request will 404.
func LoadConfig() Config {
	var cfg Config
	if err := viper.UnmarshalKey("gateway", &cfg); err != nil {
		logrus.WithError(err).Error("gateway: failed to unmarshal [gateway] config; continuing with defaults")
	}
	return cfg
}
