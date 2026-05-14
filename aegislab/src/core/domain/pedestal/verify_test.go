package pedestal

import (
	"errors"
	"os"
	"testing"
)

// stubRunner implements Runner for testing without shelling out to helm.
type stubRunner struct {
	repoAddErr    error
	repoUpdateErr error
	pullErr       error
}

func (s stubRunner) RepoAdd(name, url string) (string, error) { return "", s.repoAddErr }
func (s stubRunner) RepoUpdate() (string, error)              { return "", s.repoUpdateErr }
func (s stubRunner) Pull(repo, chart, version, destDir string) (string, error) {
	return "", s.pullErr
}

func noopVerifier(_ string) error { return nil }

func baseCfg() Config {
	return Config{
		ChartName: "my-chart",
		Version:   "1.0.0",
		RepoURL:   "https://example.com/charts",
		RepoName:  "example",
	}
}

func TestRunAllChecksPass(t *testing.T) {
	result := Run(stubRunner{}, baseCfg(), noopVerifier)
	if !result.OK {
		t.Fatalf("expected OK=true, got false; checks: %+v", result.Checks)
	}
	if len(result.Checks) != 3 {
		t.Fatalf("expected 3 checks (repo_add, repo_update, helm_pull), got %d", len(result.Checks))
	}
	for _, c := range result.Checks {
		if !c.OK {
			t.Errorf("check %q should be OK", c.Name)
		}
	}
}

func TestRunRepoAddFailureShortCircuits(t *testing.T) {
	runner := stubRunner{repoAddErr: errors.New("auth failed")}
	result := Run(runner, baseCfg(), noopVerifier)
	if result.OK {
		t.Fatal("expected OK=false after repo_add failure")
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(result.Checks))
	}
	if result.Checks[0].Name != "repo_add" {
		t.Errorf("expected check name repo_add, got %q", result.Checks[0].Name)
	}
}

func TestRunRepoUpdateFailureShortCircuits(t *testing.T) {
	runner := stubRunner{repoUpdateErr: errors.New("network timeout")}
	result := Run(runner, baseCfg(), noopVerifier)
	if result.OK {
		t.Fatal("expected OK=false after repo_update failure")
	}
	if len(result.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(result.Checks))
	}
	if result.Checks[1].Name != "repo_update" || result.Checks[1].OK {
		t.Errorf("second check should be repo_update=false, got %+v", result.Checks[1])
	}
}

func TestRunPullFailureContinuesToValueFile(t *testing.T) {
	cfg := baseCfg()
	cfg.ValueFile = "/nonexistent/values.yaml"

	runner := stubRunner{pullErr: errors.New("chart not found")}
	result := Run(runner, cfg, noopVerifier)
	if result.OK {
		t.Fatal("expected OK=false when pull fails")
	}
	// Should have: repo_add(ok), repo_update(ok), helm_pull(fail), value_file(fail)
	if len(result.Checks) < 3 {
		t.Fatalf("expected at least 3 checks, got %d", len(result.Checks))
	}
	pullCheck := result.Checks[2]
	if pullCheck.Name != "helm_pull" || pullCheck.OK {
		t.Errorf("expected helm_pull=false, got %+v", pullCheck)
	}
}

func TestVerifyValueFileValidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "values-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString("replicaCount: 3\nimage:\n  repository: nginx\n  tag: latest\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := VerifyValueFile(f.Name()); err != nil {
		t.Fatalf("expected no error for valid YAML, got: %v", err)
	}
}

func TestVerifyValueFileInvalidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "values-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString("{{{{not valid yaml"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := VerifyValueFile(f.Name()); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestVerifyValueFileNonexistent(t *testing.T) {
	err := VerifyValueFile("/tmp/this-file-does-not-exist-aegis-test.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestRunWithValueFileValid(t *testing.T) {
	f, err := os.CreateTemp("", "values-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString("key: value\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg := baseCfg()
	cfg.ValueFile = f.Name()

	result := Run(stubRunner{}, cfg, VerifyValueFile)
	if !result.OK {
		t.Fatalf("expected OK=true, got false; checks: %+v", result.Checks)
	}
	// Should have 4 checks: repo_add, repo_update, helm_pull, value_file
	if len(result.Checks) != 4 {
		t.Fatalf("expected 4 checks, got %d", len(result.Checks))
	}
	if last := result.Checks[3]; last.Name != "value_file" || !last.OK {
		t.Errorf("expected value_file=true, got %+v", last)
	}
}

func TestRunWithValueFileInvalid(t *testing.T) {
	f, err := os.CreateTemp("", "values-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString("{{bad"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg := baseCfg()
	cfg.ValueFile = f.Name()

	result := Run(stubRunner{}, cfg, VerifyValueFile)
	if result.OK {
		t.Fatal("expected OK=false for invalid value file")
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "value_file" {
			found = true
			if c.OK {
				t.Error("value_file check should be false")
			}
		}
	}
	if !found {
		t.Error("value_file check not present")
	}
}
