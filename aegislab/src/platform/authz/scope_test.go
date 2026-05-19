package authz

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"aegis/platform/consts"

	"github.com/gin-gonic/gin"
)

func TestSystemScopeAllowsAllProjects(t *testing.T) {
	s := SystemScope()
	if !s.AllowsAllProjects() {
		t.Fatal("SystemScope must allow all projects")
	}
	if err := s.MustHaveProject(42); err != nil {
		t.Fatalf("SystemScope.MustHaveProject: %v", err)
	}
}

func TestMustHaveProjectAdminMatchMiss(t *testing.T) {
	cases := []struct {
		name    string
		scope   CallerScope
		project int64
		wantErr bool
	}{
		{"admin sees any project", CallerScope{IsAdmin: true}, 99, false},
		{"member sees visible", CallerScope{VisibleProjects: []int64{7}}, 7, false},
		{"member rejected for unscoped", CallerScope{VisibleProjects: []int64{7}}, 9, true},
		{"empty non-admin rejected", CallerScope{}, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.scope.MustHaveProject(tc.project)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrProjectOutsideScope) {
				t.Fatalf("want ErrProjectOutsideScope, got %v", err)
			}
		})
	}
}

type stubResolver struct {
	projects []int64
	err      error
}

func (s stubResolver) VisibleProjects(_ context.Context, _ int64) ([]int64, error) {
	return s.projects, s.err
}

func newGinCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c
}

func TestScopeFromGinContextHappyPath(t *testing.T) {
	c := newGinCtx()
	c.Set(consts.CtxKeyUserID, 5)
	c.Set(consts.CtxKeyIsAdmin, false)

	got, err := ScopeFromGinContext(c, stubResolver{projects: []int64{3, 4}})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.UserID != 5 || got.IsAdmin {
		t.Fatalf("scope=%+v", got)
	}
	if len(got.VisibleProjects) != 2 || got.VisibleProjects[0] != 3 {
		t.Fatalf("visible=%v", got.VisibleProjects)
	}

	// Cached: a second call must not re-invoke the resolver. Pass a resolver
	// that would error to prove the cached path runs.
	got2, err := ScopeFromGinContext(c, stubResolver{err: errors.New("must not be called")})
	if err != nil || got2.UserID != 5 {
		t.Fatalf("cached path broken: %+v err=%v", got2, err)
	}
}

func TestScopeFromGinContextAdminSkipsResolver(t *testing.T) {
	c := newGinCtx()
	c.Set(consts.CtxKeyUserID, 1)
	c.Set(consts.CtxKeyIsAdmin, true)

	got, err := ScopeFromGinContext(c, stubResolver{err: errors.New("must not be called")})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !got.IsAdmin {
		t.Fatal("admin scope lost")
	}
}

func TestScopeFromGinContextMissingAuth(t *testing.T) {
	c := newGinCtx()
	_, err := ScopeFromGinContext(c, stubResolver{})
	if !errors.Is(err, ErrMissingAuth) {
		t.Fatalf("want ErrMissingAuth, got %v", err)
	}
}
