package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
)

// AuditConfig configures the gateway's on-disk access + audit trail.
// Both paths are optional and independent; an empty path disables that
// sink (records still go to the structured stdout logger).
type AuditConfig struct {
	// AccessLogPath receives one JSON line per proxied request (every
	// method, including denied ones).
	AccessLogPath string `mapstructure:"access_log_path"`
	// AuditLogPath receives one JSON line per state-changing request
	// (anything other than GET/HEAD/OPTIONS) carrying the resolved
	// identity and the allow/deny decision.
	AuditLogPath string `mapstructure:"audit_log_path"`
}

// AuditEvent is one structured record written to disk. The access sink
// receives every event; the audit sink receives only the mutating subset.
type AuditEvent struct {
	Time         string `json:"time"`
	RequestID    string `json:"request_id"`
	Route        string `json:"route"`
	Upstream     string `json:"upstream"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Status       int    `json:"status"`
	LatencyMS    int64  `json:"latency_ms"`
	ClientIP     string `json:"client_ip"`
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	Roles        string `json:"roles"`
	IsAdmin      string `json:"is_admin"`
	AuthType     string `json:"auth_type"`
	AuthDecision string `json:"auth_decision"` // "allow" | "deny"
}

// AuditSink fans structured events to up to two append-only JSON-lines
// files. It is safe for concurrent use and degrades to a no-op when a
// path is unset or cannot be opened, so a logging failure never takes
// the request path down.
type AuditSink struct {
	mu     sync.Mutex
	access *os.File
	audit  *os.File
}

// NewAuditSink opens the configured files. Failures are logged and leave
// that sink disabled rather than failing the gateway boot.
func NewAuditSink(cfg AuditConfig) *AuditSink {
	return &AuditSink{
		access: openAppend(cfg.AccessLogPath, "access"),
		audit:  openAppend(cfg.AuditLogPath, "audit"),
	}
}

func openAppend(path, kind string) *os.File {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			logrus.WithError(err).WithField("path", path).Errorf("gateway: cannot create %s log dir", kind)
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		logrus.WithError(err).WithField("path", path).Errorf("gateway: cannot open %s log", kind)
		return nil
	}
	logrus.WithField("path", path).Infof("gateway: %s log → disk", kind)
	return f
}

// Record writes ev to the access sink, and additionally to the audit
// sink when mutating is true.
func (s *AuditSink) Record(ev AuditEvent, mutating bool) {
	if s == nil || (s.access == nil && s.audit == nil) {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access != nil {
		_, _ = s.access.Write(line)
	}
	if mutating && s.audit != nil {
		_, _ = s.audit.Write(line)
	}
}

// Close flushes and closes both files.
func (s *AuditSink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access != nil {
		_ = s.access.Close()
		s.access = nil
	}
	if s.audit != nil {
		_ = s.audit.Close()
		s.audit = nil
	}
	return nil
}
