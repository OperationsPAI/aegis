package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeChartExec captures invocations and returns scripted results.
type fakeChartExec struct {
	lookPath map[string]error           // name -> err (nil = found)
	results  map[string]fakeExecResult  // "kubectl get pods..." -> result
	calls    [][]string                 // record of {name, args...} for assertions
	fallback func(name string, args []string) ([]byte, error)
}

type fakeExecResult struct {
	out []byte
	err error
}

func (f *fakeChartExec) LookPath(name string) (string, error) {
	if f.lookPath == nil {
		return "/usr/bin/" + name, nil
	}
	err, ok := f.lookPath[name]
	if !ok {
		return "/usr/bin/" + name, nil
	}
	if err != nil {
		return "", err
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeChartExec) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	key := name + " " + strings.Join(args, " ")
	if r, ok := f.results[key]; ok {
		return r.out, r.err
	}
	if f.fallback != nil {
		return f.fallback(name, args)
	}
	return nil, nil
}

func withFakeRunner(t *testing.T, f *fakeChartExec) {
	t.Helper()
	old := chartRunner
	chartRunner = f
	t.Cleanup(func() { chartRunner = old })
}

// TestPedestalChartPushValidation covers argument validation: missing --tgz,
// --tgz pointing at a file that does not exist, and --tgz pointing at a dir.
func TestPedestalChartPushValidation(t *testing.T) {
	t.Run("missing_name", func(t *testing.T) {
		err := runPedestalChartPush("", "/tmp/x.tgz", "", "")
		if err == nil || !strings.Contains(err.Error(), "--name") {
			t.Fatalf("want --name error, got %v", err)
		}
	})

	t.Run("missing_tgz", func(t *testing.T) {
		err := runPedestalChartPush("ts", "", "", "")
		if err == nil || !strings.Contains(err.Error(), "--tgz") {
			t.Fatalf("want --tgz error, got %v", err)
		}
	})

	t.Run("tgz_not_found", func(t *testing.T) {
		err := runPedestalChartPush("ts", "/does/not/exist/chart.tgz", "", "")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("want not-found error, got %v", err)
		}
	})

	t.Run("tgz_is_directory", func(t *testing.T) {
		dir := t.TempDir()
		err := runPedestalChartPush("ts", dir, "", "")
		if err == nil || !strings.Contains(err.Error(), "must be a file") {
			t.Fatalf("want must-be-a-file error, got %v", err)
		}
	})

	t.Run("kubectl_missing", func(t *testing.T) {
		f := &fakeChartExec{lookPath: map[string]error{"kubectl": errors.New("not found")}}
		withFakeRunner(t, f)
		tgz := filepath.Join(t.TempDir(), "ts-1.0.0.tgz")
		if err := os.WriteFile(tgz, []byte("pkg"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := runPedestalChartPush("ts", tgz, "producer-0", "aegislab-backend")
		if err == nil || !strings.Contains(err.Error(), "kubectl not found") {
			t.Fatalf("want kubectl-missing error, got %v", err)
		}
	})
}

// TestPedestalChartPushCopiesAndVerifies drives the happy path with an
// explicit --producer-pod so no label lookup is needed, and asserts the
// recorded kubectl cp + verify invocations.
func TestPedestalChartPushCopiesAndVerifies(t *testing.T) {
	tgz := filepath.Join(t.TempDir(), "ts-1.0.0.tgz")
	if err := os.WriteFile(tgz, []byte("pkg"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeChartExec{
		fallback: func(name string, args []string) ([]byte, error) {
			if len(args) >= 4 && args[2] == "exec" {
				return []byte("-rw-r--r-- 1 root root 3 /var/lib/rcabench/dataset/charts/ts-1.0.0.tgz\n"), nil
			}
			return nil, nil
		},
	}
	withFakeRunner(t, f)

	if err := runPedestalChartPush("ts", tgz, "aegislab-producer-0", "aegislab-backend"); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	if len(f.calls) != 2 {
		t.Fatalf("want 2 kubectl calls (cp + ls), got %d: %v", len(f.calls), f.calls)
	}
	cp := f.calls[0]
	if cp[0] != "kubectl" || cp[3] != "cp" {
		t.Fatalf("first call should be kubectl ... cp, got %v", cp)
	}
	wantDst := "aegislab-producer-0:/var/lib/rcabench/dataset/charts/ts-1.0.0.tgz"
	if cp[len(cp)-1] != wantDst {
		t.Fatalf("wrong dst: got %q want %q", cp[len(cp)-1], wantDst)
	}
}

// TestNsPatternToNamespace covers the regex -> namespace derivation used by
// `chart install` when --namespace is omitted.
func TestNsPatternToNamespace(t *testing.T) {
	cases := []struct {
		pattern string
		idx     int
		want    string
	}{
		{`^ts\d+$`, 0, "ts0"},
		{`^ts\d+$`, 3, "ts3"},
		{`^app-\d+$`, 0, "app-0"},
		{`^test_\d+_suffix$`, 2, "test_2_suffix"},
		{`^literal-ns$`, 0, "literal-ns"},
		{`literal-ns`, 0, "literal-ns"},
		{``, 0, ""},
	}
	for _, tc := range cases {
		got := nsPatternToNamespace(tc.pattern, tc.idx)
		if got != tc.want {
			t.Errorf("nsPatternToNamespace(%q, %d) = %q, want %q", tc.pattern, tc.idx, got, tc.want)
		}
	}
}

// TestPedestalChartInstallValidation covers the require-tgz guard and the
// helm-missing guard. Namespace derivation against the live backend is not
// exercised here (requires /api/v2/systems plumbing); it's unit-covered above
// via TestNsPatternToNamespace.
func TestPedestalChartInstallValidation(t *testing.T) {
	t.Run("missing_code", func(t *testing.T) {
		err := runPedestalChartInstall("", "ts0", "/tmp/x.tgz", "", false)
		if err == nil || !strings.Contains(err.Error(), "short-code") {
			t.Fatalf("want short-code error, got %v", err)
		}
	})

	t.Run("tgz_required_when_namespace_given", func(t *testing.T) {
		err := runPedestalChartInstall("ts", "ts0", "", "", false)
		if err == nil || !strings.Contains(err.Error(), "--tgz is required") {
			t.Fatalf("want --tgz-required error, got %v", err)
		}
	})

	t.Run("tgz_not_found", func(t *testing.T) {
		err := runPedestalChartInstall("ts", "ts0", "/does/not/exist.tgz", "", false)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("want not-found error, got %v", err)
		}
	})

	t.Run("helm_missing", func(t *testing.T) {
		f := &fakeChartExec{lookPath: map[string]error{"helm": errors.New("not found")}}
		withFakeRunner(t, f)
		tgz := filepath.Join(t.TempDir(), "chart.tgz")
		if err := os.WriteFile(tgz, []byte("pkg"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := runPedestalChartInstall("ts", "ts0", tgz, "", false)
		if err == nil || !strings.Contains(err.Error(), "helm not found") {
			t.Fatalf("want helm-missing error, got %v", err)
		}
	})

	t.Run("happy_path_invokes_helm_install", func(t *testing.T) {
		f := &fakeChartExec{
			fallback: func(name string, args []string) ([]byte, error) {
				return []byte("Release \"ts0\" installed\n"), nil
			},
		}
		withFakeRunner(t, f)
		tgz := filepath.Join(t.TempDir(), "chart.tgz")
		if err := os.WriteFile(tgz, []byte("pkg"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := runPedestalChartInstall("ts", "ts0", tgz, "1.2.3", true); err != nil {
			t.Fatalf("install failed: %v", err)
		}
		if len(f.calls) != 1 {
			t.Fatalf("want 1 helm call, got %d: %v", len(f.calls), f.calls)
		}
		call := f.calls[0]
		if call[0] != "helm" || call[1] != "install" || call[2] != "ts0" {
			t.Fatalf("bad helm args: %v", call)
		}
		joined := strings.Join(call, " ")
		for _, want := range []string{"--create-namespace", "--version 1.2.3", "--wait", "-n ts0"} {
			if !strings.Contains(joined, want) {
				t.Errorf("helm args missing %q: %s", want, joined)
			}
		}
	})
}
