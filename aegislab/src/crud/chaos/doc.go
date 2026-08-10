// Package chaos owns the chaos-experiment seed catalog, renderers, and
// guided-mode validators. ChaosTypeToCapability is generated from
// aegislab/tools/capgen — re-run with `just capgen` or via `go generate`.

//go:generate bash -c "cd ../../../tools/capgen && go run . -go-out ../../src/crud/chaos/capability_map_gen.go output"

package chaos
