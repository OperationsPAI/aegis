package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStalePodChaosLister struct {
	traces []string
	err    error
}

func (f *fakeStalePodChaosLister) ListPodChaosTraceIDs(_ context.Context, _ string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.traces, nil
}

func TestWarnStalePodChaos_NoneSilent(t *testing.T) {
	var buf bytes.Buffer
	err := warnStalePodChaos(context.Background(), "ob0", &fakeStalePodChaosLister{}, &buf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected silent stderr, got %q", buf.String())
	}
}

func TestWarnStalePodChaos_EmptyNamespaceNoOp(t *testing.T) {
	var buf bytes.Buffer
	lister := &fakeStalePodChaosLister{err: errors.New("should not be called")}
	if err := warnStalePodChaos(context.Background(), "", lister, &buf); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected silent stderr, got %q", buf.String())
	}
}

func TestWarnStalePodChaos_WithTraces(t *testing.T) {
	var buf bytes.Buffer
	lister := &fakeStalePodChaosLister{traces: []string{"a1b2c3", "d4e5f6", "7890ab"}}
	if err := warnStalePodChaos(context.Background(), "ob0", lister, &buf); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "WARN: 3 PodChaos CR(s) in ns=ob0") {
		t.Fatalf("missing WARN header: %q", out)
	}
	for _, id := range []string{"a1b2c3", "d4e5f6", "7890ab"} {
		if !strings.Contains(out, id) {
			t.Fatalf("missing trace id %s in output: %q", id, out)
		}
	}
	if !strings.Contains(out, "aegisctl trace cancel") {
		t.Fatalf("missing cleanup hint: %q", out)
	}
}

func TestWarnStalePodChaos_DedupesAndFiltersEmpty(t *testing.T) {
	var buf bytes.Buffer
	lister := &fakeStalePodChaosLister{traces: []string{"aaa", "", "bbb", "aaa"}}
	if err := warnStalePodChaos(context.Background(), "ob0", lister, &buf); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "WARN: 2 PodChaos CR(s)") {
		t.Fatalf("expected count=2 after dedupe/filter, got: %q", out)
	}
}

func TestWarnStalePodChaos_TruncatesOverTen(t *testing.T) {
	traces := []string{}
	for i := 0; i < 13; i++ {
		traces = append(traces, string(rune('a'+i))+"00000")
	}
	var buf bytes.Buffer
	lister := &fakeStalePodChaosLister{traces: traces}
	if err := warnStalePodChaos(context.Background(), "ob0", lister, &buf); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "WARN: 13 PodChaos CR(s)") {
		t.Fatalf("expected total=13: %q", out)
	}
	if !strings.Contains(out, "and 3 more") {
		t.Fatalf("expected truncation marker: %q", out)
	}
}

func TestWarnStalePodChaos_ClusterUnreachableInfoAndContinues(t *testing.T) {
	var buf bytes.Buffer
	lister := &fakeStalePodChaosLister{err: errors.New("connection refused")}
	if err := warnStalePodChaos(context.Background(), "ob0", lister, &buf); err != nil {
		t.Fatalf("must not fail submit on unreachable cluster, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "info: skipped stale-CRD check") {
		t.Fatalf("expected info line, got: %q", out)
	}
	if strings.Contains(out, "WARN:") {
		t.Fatalf("must not emit WARN when unreachable: %q", out)
	}
}
