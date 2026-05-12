package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// newTestGateway returns a gateway pointed at a Redis on REDIS_HOST or
// localhost:6379, or skips the test if no server answers within 200ms.
// This keeps `go test ./infra/redis/...` green in environments without
// Redis (e.g. minimal CI) while still exercising the Lua script when a
// Redis is up.
func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	addr := "localhost:6379"
	client := goredis.NewClient(&goredis.Options{Addr: addr, DB: 1})
	pingCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("redis not available at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &Gateway{client: client}
}

// TestCompareAndDeleteKey covers the Lua release primitive used by the
// #166 namespace allocator. It must (a) delete the key when the stored
// value matches the supplied value, (b) leave the key untouched when the
// stored value differs (the race-safety property), and (c) return 0
// without error when the key has already expired.
func TestCompareAndDeleteKey(t *testing.T) {
	g := newTestGateway(t)
	ctx := context.Background()
	key := "test:cad:" + t.Name()
	t.Cleanup(func() { _, _ = g.DeleteKey(ctx, key) })

	// (a) value matches — delete.
	if err := g.Set(ctx, key, "owner-A", time.Minute); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	n, err := g.CompareAndDeleteKey(ctx, key, "owner-A")
	if err != nil {
		t.Fatalf("CompareAndDeleteKey(matching): %v", err)
	}
	if n != 1 {
		t.Fatalf("matching delete count = %d, want 1", n)
	}
	exists, err := g.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after matching delete: %v", err)
	}
	if exists {
		t.Fatalf("key still present after matching CompareAndDeleteKey")
	}

	// (b) value differs — must NOT delete. This is the race-safety
	// property: a slow allocator releasing after its TTL expired must
	// not blow away a successor's lock.
	if err := g.Set(ctx, key, "owner-B", time.Minute); err != nil {
		t.Fatalf("seed key for mismatch case: %v", err)
	}
	n, err = g.CompareAndDeleteKey(ctx, key, "owner-A")
	if err != nil {
		t.Fatalf("CompareAndDeleteKey(mismatch): %v", err)
	}
	if n != 0 {
		t.Fatalf("mismatch delete count = %d, want 0", n)
	}
	exists, err = g.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after mismatch: %v", err)
	}
	if !exists {
		t.Fatalf("key was deleted despite value mismatch — race safety broken")
	}

	// (c) key absent — no error, count 0.
	if _, err := g.DeleteKey(ctx, key); err != nil {
		t.Fatalf("cleanup before absent case: %v", err)
	}
	n, err = g.CompareAndDeleteKey(ctx, key, "owner-A")
	if err != nil {
		t.Fatalf("CompareAndDeleteKey(absent): %v", err)
	}
	if n != 0 {
		t.Fatalf("absent delete count = %d, want 0", n)
	}
}
