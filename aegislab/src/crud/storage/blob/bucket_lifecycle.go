package blob

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// BucketLifecycle is the per-bucket lifecycle policy persisted alongside
// the bucket configuration. v1 keeps the shape intentionally tiny — a
// flat list of rules, each with one action — so the schema can evolve
// once execution lands (see lifecycleExecutionDeferred below) without
// committing to a full S3-style policy in DB-on-disk JSON.
//
// lifecycleExecutionDeferred: persistence only. The DeletionWorker in
// lifecycle.go sweeps by ObjectRecord.ExpiresAt; it does NOT consume
// BucketLifecycle.Rules yet. A future task (tracked alongside REQ-830)
// will wire a rule evaluator into the sweep.
type BucketLifecycle struct {
	Rules []BucketLifecycleRule `json:"rules"`
}

// BucketLifecycleRule describes one named lifecycle rule. v1 supports
// only the "delete" action and only prefix-matching against the object
// storage key.
type BucketLifecycleRule struct {
	Name            string `json:"name"`
	MatchPrefix     string `json:"match_prefix"`
	ExpireAfterDays int    `json:"expire_after_days"`
	Action          string `json:"action"`
}

// Validation bounds for a lifecycle config. Tight on purpose — a 4 KB
// JSON column is plenty for ≤50 rules, but the per-field caps stop a
// caller from stuffing a 4 KB prefix into a single rule.
const (
	BucketLifecycleMaxRules         = 50
	BucketLifecycleMaxPrefixLen     = 256
	BucketLifecycleMinExpireDays    = 1
	BucketLifecycleMaxExpireDays    = 3650
	BucketLifecycleActionDelete     = "delete"
	bucketLifecycleMaxNameLen       = 64
	bucketLifecycleConfigJSONCapKiB = 4 // matches the DB column cap
)

// ErrBucketLifecycleInvalid is the sentinel returned by Validate. Tests
// branch on errors.Is.
var ErrBucketLifecycleInvalid = errors.New("invalid bucket lifecycle")

// Validate enforces the v1 invariants: ≤50 rules, action ∈ {"delete"},
// expire_after_days ∈ [1, 3650], match_prefix ≤ 256 chars, unique
// non-empty names, encoded form ≤ 4 KB.
func (l *BucketLifecycle) Validate() error {
	if l == nil {
		return nil
	}
	if len(l.Rules) > BucketLifecycleMaxRules {
		return fmt.Errorf("%w: %d rules exceeds cap %d",
			ErrBucketLifecycleInvalid, len(l.Rules), BucketLifecycleMaxRules)
	}
	seen := make(map[string]struct{}, len(l.Rules))
	for i := range l.Rules {
		r := &l.Rules[i]
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("%w: rule %d: name is required", ErrBucketLifecycleInvalid, i)
		}
		if len(name) > bucketLifecycleMaxNameLen {
			return fmt.Errorf("%w: rule %q: name exceeds %d chars",
				ErrBucketLifecycleInvalid, name, bucketLifecycleMaxNameLen)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("%w: duplicate rule name %q", ErrBucketLifecycleInvalid, name)
		}
		seen[name] = struct{}{}
		if len(r.MatchPrefix) > BucketLifecycleMaxPrefixLen {
			return fmt.Errorf("%w: rule %q: match_prefix exceeds %d chars",
				ErrBucketLifecycleInvalid, name, BucketLifecycleMaxPrefixLen)
		}
		if r.ExpireAfterDays < BucketLifecycleMinExpireDays || r.ExpireAfterDays > BucketLifecycleMaxExpireDays {
			return fmt.Errorf("%w: rule %q: expire_after_days %d out of range [%d,%d]",
				ErrBucketLifecycleInvalid, name, r.ExpireAfterDays,
				BucketLifecycleMinExpireDays, BucketLifecycleMaxExpireDays)
		}
		if r.Action != BucketLifecycleActionDelete {
			return fmt.Errorf("%w: rule %q: action %q not in {%q}",
				ErrBucketLifecycleInvalid, name, r.Action, BucketLifecycleActionDelete)
		}
		r.Name = name
	}
	enc, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrBucketLifecycleInvalid, err)
	}
	if len(enc) > bucketLifecycleConfigJSONCapKiB*1024 {
		return fmt.Errorf("%w: encoded size %d > %d KB cap",
			ErrBucketLifecycleInvalid, len(enc), bucketLifecycleConfigJSONCapKiB)
	}
	return nil
}

// encodeBucketLifecycle marshals a lifecycle policy for storage in the
// DB column. A nil or empty-rules policy serializes to "" so a row
// without a configured lifecycle stays empty rather than a stub "{}".
func encodeBucketLifecycle(l *BucketLifecycle) (string, error) {
	if l == nil || len(l.Rules) == 0 {
		return "", nil
	}
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeBucketLifecycle is the inverse of encodeBucketLifecycle. Empty
// strings round-trip to a nil policy.
func decodeBucketLifecycle(s string) (*BucketLifecycle, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out BucketLifecycle
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
