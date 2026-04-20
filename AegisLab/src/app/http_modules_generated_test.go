package app

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestGeneratedProducerHTTPModuleRegistryMatchesModuleDirs(t *testing.T) {
	moduleRoot := filepath.Join("..", "module")
	entries, err := os.ReadDir(moduleRoot)
	if err != nil {
		t.Fatalf("read module dir: %v", err)
	}

	var discovered []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(moduleRoot, entry.Name(), "module.go")); err == nil {
			discovered = append(discovered, entry.Name())
		}
	}
	sort.Strings(discovered)

	if !reflect.DeepEqual(discovered, producerHTTPModuleNames()) {
		t.Fatalf("generated producer HTTP module registry is stale\nwant: %v\ngot:  %v", discovered, producerHTTPModuleNames())
	}
}

func TestProducerHTTPModuleRegistryAllowsDeletingOneModule(t *testing.T) {
	srcRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve src root: %v", err)
	}
	projectRoot := filepath.Dir(srcRoot)

	scratchRoot := filepath.Join(t.TempDir(), "AegisLab")
	if err := copyTree(srcRoot, filepath.Join(scratchRoot, "src")); err != nil {
		t.Fatalf("copy src tree: %v", err)
	}
	if err := os.Symlink(filepath.Join(projectRoot, "..", "chaos-experiment"), filepath.Join(filepath.Dir(scratchRoot), "chaos-experiment")); err != nil {
		t.Fatalf("link chaos-experiment sibling for scratch tree: %v", err)
	}

	generatorSrc := filepath.Join(projectRoot, "scripts", "generate_http_modules.py")
	generatorDst := filepath.Join(scratchRoot, "scripts", "generate_http_modules.py")
	if err := os.MkdirAll(filepath.Dir(generatorDst), 0o755); err != nil {
		t.Fatalf("create scratch scripts dir: %v", err)
	}
	if err := copyFile(generatorSrc, generatorDst, 0o755); err != nil {
		t.Fatalf("copy generator: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(scratchRoot, "src", "module", "widget")); err != nil {
		t.Fatalf("remove widget module from scratch tree: %v", err)
	}

	scratchSrc := filepath.Join(scratchRoot, "src")
	runCommand(t, scratchSrc, "python3", "../scripts/generate_http_modules.py")
	runCommand(t, scratchSrc, "go", "test", "./app", "-run", "TestProducerOptionsValidate", "-count=1")
}

func runCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(output))
	}
}

func copyTree(srcRoot, dstRoot string) error {
	return filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
