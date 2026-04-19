package group

import (
	"aegis/framework"
	redisinfra "aegis/infra/redis"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	return db
}

func TestModuleProvidesRouteRegistrar(t *testing.T) {
	var routes []framework.RouteRegistrar

	app := fxtest.New(t,
		fx.Provide(func() *gorm.DB { return testDB(t) }),
		fx.Provide(func() *redisinfra.Gateway { return redisinfra.NewGateway(nil) }),
		Module,
		fx.Invoke(fx.Annotate(
			func(r []framework.RouteRegistrar) { routes = r },
			fx.ParamTags(`group:"routes"`),
		)),
	)
	defer app.RequireStart().RequireStop()

	if len(routes) != 1 {
		t.Fatalf("expected 1 route registrar, got %d", len(routes))
	}
	if routes[0].Audience != framework.AudiencePortal {
		t.Fatalf("expected AudiencePortal, got %q", routes[0].Audience)
	}
	if routes[0].Name != "group" {
		t.Fatalf("expected name %q, got %q", "group", routes[0].Name)
	}
}
