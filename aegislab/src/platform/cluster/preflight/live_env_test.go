package preflight

import (
	"sync"
	"testing"
)

// TestLiveEnvAccessorsAreRaceFreeOnFirstUse exists because LiveEnv is a
// process-wide singleton serving concurrent HTTP requests; an earlier
// design did `if e.k8s == nil { e.k8s = ... }` on shared fields with no
// synchronization, so two concurrent first-time callers both observed
// nil and both wrote. Run with `go test -race` to catch any
// regression. The probe wrappers are now constructed eagerly in
// NewLiveEnv; this test pins that behavior.
func TestLiveEnvAccessorsAreRaceFreeOnFirstUse(t *testing.T) {
	env := NewLiveEnv(Config{})

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = env.Net()
			_ = env.K8s()
			_ = env.ClickHouse()
			_ = env.MySQL()
			_ = env.Redis()
			_ = env.Etcd()
			_ = env.Helm()
		}()
	}
	wg.Wait()
}
