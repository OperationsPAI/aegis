package chaos

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// PointIdentity carries the tuple required to derive a service-bound
// Point ID per ADR-0011.
type PointIdentity struct {
	System       string
	Service      string
	Instance     string
	ChartVersion string
	Capability   string
	Target       map[string]any
}

// CrossServicePointIdentity is the cross-service variant (point.service_id
// IS NULL). Per design §4, only (system, capability, target) participates.
type CrossServicePointIdentity struct {
	System     string
	Capability string
	Target     map[string]any
}

func ServiceBoundPointID(p PointIdentity) (string, error) {
	tj, err := canonicalTargetJSON(p.Target)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(
		p.System + "/" + p.Service + "/" + p.Instance + "/" +
			p.ChartVersion + "/" + p.Capability + "/" + tj))
	return hex.EncodeToString(h[:])[:16], nil
}

func CrossServicePointID(p CrossServicePointIdentity) (string, error) {
	tj, err := canonicalTargetJSON(p.Target)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(p.System + "/" + p.Capability + "/" + tj))
	return hex.EncodeToString(h[:])[:16], nil
}

// canonicalTargetJSON canonicalises a Point's target for hashing with the
// namespace key removed. namespace is a runtime binding (the concrete
// per-instance allocator namespace the CR is applied into), not part of a
// Point's abstract identity — a catalog Point must match an injection
// resolving in any of its system's namespaces. import and inject MUST share
// this single path; any divergence reintroduces point_id mismatches.
func canonicalTargetJSON(target map[string]any) (string, error) {
	stripped := make(map[string]any, len(target))
	for k, v := range target {
		if k == "namespace" {
			continue
		}
		stripped[k] = v
	}
	return canonicalJSON(stripped)
}

// canonicalJSON serialises a map with object keys sorted lexicographically
// at every level. Required so that two callers producing the same logical
// target get the same point_id.
func canonicalJSON(v any) (string, error) {
	c, err := canonicalize(v)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return "", err
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return string(out), nil
}

func canonicalize(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := sortedMap{keys: keys, values: make(map[string]any, len(keys))}
		for _, k := range keys {
			c, err := canonicalize(x[k])
			if err != nil {
				return nil, err
			}
			out.values[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			c, err := canonicalize(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case string, bool, float64, int, int32, int64, float32, json.Number:
		return x, nil
	default:
		return nil, fmt.Errorf("chaos.canonicalize: unsupported type %T", v)
	}
}

type sortedMap struct {
	keys   []string
	values map[string]any
}

func (s sortedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range s.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(s.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
