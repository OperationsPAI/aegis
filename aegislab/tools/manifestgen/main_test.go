package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	manifestSchemaPath = "../../src/cli/cmd/manifest_schema.json"
	capabilitiesPath   = "../capgen/output/capabilities.json"
	chaosRoot          = "../../../chaos-experiment/internal"
)

// generateOnce produces all manifests into a tmp dir once per test process.
// Both tests reuse the same artifacts so we don't pay parse cost twice.
var (
	tmpOutOnce string
	tmpOutErr  error
	tmpOutDone bool
)

func ensureGenerated(t *testing.T) string {
	if tmpOutDone {
		if tmpOutErr != nil {
			t.Fatal(tmpOutErr)
		}
		return tmpOutOnce
	}
	dir, err := os.MkdirTemp("", "manifestgen-test-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpOutOnce = dir
	tmpOutErr = run(chaosRoot, dir, defaultChartVersion)
	tmpOutDone = true
	if tmpOutErr != nil {
		t.Fatal(tmpOutErr)
	}
	return dir
}

func walkManifests(t *testing.T, root string, fn func(path string, doc map[string]any)) {
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(p) != ".yaml" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		j, err := sigsyaml.YAMLToJSON(raw)
		if err != nil {
			return fmt.Errorf("%s: yaml→json: %w", p, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(j, &doc); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		fn(p, doc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAllManifestsValidate(t *testing.T) {
	dir := ensureGenerated(t)

	schemaBytes, err := os.ReadFile(manifestSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schemaDoc any
	if err := json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("file:///manifest-schema.json", schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("file:///manifest-schema.json")
	if err != nil {
		t.Fatal(err)
	}

	count := 0
	walkManifests(t, dir, func(path string, doc map[string]any) {
		if err := schema.Validate(doc); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		count++
	})
	if count == 0 {
		t.Fatal("no manifests generated")
	}
	t.Logf("validated %d manifests", count)
}

func TestTargetSchemasMatch(t *testing.T) {
	dir := ensureGenerated(t)

	capRaw, err := os.ReadFile(capabilitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	var caps []struct {
		Name         string `json:"name"`
		TargetSchema any    `json:"target_schema"`
	}
	if err := json.Unmarshal(capRaw, &caps); err != nil {
		t.Fatal(err)
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, c := range caps {
		comp := jsonschema.NewCompiler()
		uri := fmt.Sprintf("file:///cap-%s.json", c.Name)
		if err := comp.AddResource(uri, c.TargetSchema); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		s, err := comp.Compile(uri)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		compiled[c.Name] = s
	}

	pointCount := 0
	walkManifests(t, dir, func(path string, doc map[string]any) {
		spec, _ := doc["spec"].(map[string]any)
		points, _ := spec["points"].([]any)
		for i, raw := range points {
			pt, _ := raw.(map[string]any)
			cap, _ := pt["capability"].(string)
			tgt, _ := pt["target"].(map[string]any)
			s, ok := compiled[cap]
			if !ok {
				t.Errorf("%s point[%d]: unknown capability %q", path, i, cap)
				continue
			}
			if err := s.Validate(tgt); err != nil {
				t.Errorf("%s point[%d] capability=%s: %v", path, i, cap, err)
			}
			pointCount++
		}
	})
	t.Logf("validated %d points across all manifests", pointCount)
}
