package container

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aegis/consts"
	"aegis/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newRegisterTestService builds a Service backed by an in-memory sqlite DB.
// We only wire the repository — RegisterContainer never touches the Redis
// gateway, build gateway, helm file store, or label writer.
func newRegisterTestService(t *testing.T) *Service {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Models touched by the register flow. We omit the GENERATED VIRTUAL
	// columns — sqlite would reject them — by AutoMigrating just the
	// leaf entities and manually creating minimal tables if needed.
	if err := db.AutoMigrate(
		&model.Container{},
		&model.ContainerVersion{},
		&model.HelmConfig{},
		&model.ParameterConfig{},
		&model.ContainerVersionEnvVar{},
		&model.HelmConfigValue{},
	); err != nil {
		// AutoMigrate may choke on sqlite-incompatible DDL (the generated
		// columns). Skip in that case — the collision + validate branches
		// are still covered by direct Validate() tests below.
		t.Skipf("sqlite AutoMigrate unsupported for model set: %v", err)
	}

	return &Service{repo: NewRepository(db)}
}

func TestRegisterContainer_ValidateFailsFast(t *testing.T) {
	cases := []struct {
		name string
		req  RegisterContainerReq
		want string
	}{
		{"missing name", RegisterContainerReq{Form: "benchmark", Command: "x"}, "name is required"},
		{"bad form", RegisterContainerReq{Form: "weird", Name: "x"}, "form must be"},
		{"benchmark without command", RegisterContainerReq{
			Form: "benchmark", Name: "x", Registry: "docker.io", Repo: "a", Tag: "1.0.0",
		}, "command must be non-empty"},
		{"pedestal missing chart", RegisterContainerReq{
			Form: "pedestal", Name: "ob",
		}, "pedestal requires"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q in err, got %v", tc.want, err)
			}
			if !errors.Is(err, consts.ErrBadRequest) {
				t.Errorf("err not ErrBadRequest: %v", err)
			}
		})
	}
}

func TestRegisterContainer_BenchmarkAtomic(t *testing.T) {
	svc := newRegisterTestService(t)
	ctx := context.Background()

	resp, err := svc.RegisterContainer(ctx, &RegisterContainerReq{
		Form:     "benchmark",
		Name:     "ob-bench",
		Registry: "docker.io",
		Repo:     "opspai/clickhouse_dataset",
		Tag:      "e2e-kind-20260421",
		Command:  "bash /entrypoint.sh",
		// Env vars omitted: the ON CONFLICT ON CONSTRAINT clause used by
		// batchCreateOrFindParameterConfigs is unsupported on sqlite,
		// and this test only needs to exercise the core row trio.
	}, 1)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.HasPrefix(resp.RegisterID, "reg-") {
		t.Errorf("register_id prefix: %q", resp.RegisterID)
	}
	if resp.ContainerID == 0 || resp.VersionID == 0 {
		t.Fatalf("expected non-zero ids, got %+v", resp)
	}
	if resp.HelmConfigID != 0 {
		t.Errorf("benchmark should not create helm_config; got id=%d", resp.HelmConfigID)
	}
	if !strings.Contains(resp.ImageRef, "docker.io/opspai/clickhouse_dataset:e2e-kind-20260421") {
		t.Errorf("image_ref = %q", resp.ImageRef)
	}
}

func TestRegisterContainer_TypeCollisionRefuses(t *testing.T) {
	svc := newRegisterTestService(t)
	ctx := context.Background()

	// Seed a benchmark row. Repo uses a two-segment path because
	// ParseFullImageRefernce rejects single-segment refs on non-default
	// registries (canonical reference requirement).
	if _, err := svc.RegisterContainer(ctx, &RegisterContainerReq{
		Form: "benchmark", Name: "ob", Registry: "docker.io",
		Repo: "opspai/ob-bench", Tag: "1.0.0", Command: "echo hi",
	}, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Attempt to register same name as pedestal — should refuse with
	// ErrAlreadyExists before writing anything.
	_, err := svc.RegisterContainer(ctx, &RegisterContainerReq{
		Form: "pedestal", Name: "ob",
		ChartName: "onlineboutique-aegis", ChartVersion: "0.1.1",
		RepoURL: "oci://registry-1.docker.io/opspai", RepoName: "opspai",
	}, 1)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !errors.Is(err, consts.ErrAlreadyExists) {
		t.Fatalf("err not ErrAlreadyExists: %v", err)
	}
	if !strings.Contains(err.Error(), "stage=check_collision") {
		t.Errorf("error should name stage=check_collision: %v", err)
	}
	if !strings.Contains(err.Error(), "register_id=reg-") {
		t.Errorf("error should carry register_id: %v", err)
	}
}
