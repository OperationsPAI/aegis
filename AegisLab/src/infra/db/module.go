package db

import (
	"context"
	"log"
	"os"
	"time"

	"aegis/framework"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/opentelemetry/tracing"
)

var Module = fx.Module("db",
	fx.Provide(NewGormDB),
)

// Params collects both the fx lifecycle and every module-provided
// `framework.MigrationRegistrar`. Contributed entities are AutoMigrated
// alongside the central list in migration.go::centralEntities.
type Params struct {
	fx.In

	LC           fx.Lifecycle
	Contribs     []framework.MigrationRegistrar `group:"migrations"`
}

func NewGormDB(p Params) *gorm.DB {
	db := connectWithRetry(NewDatabaseConfig("mysql"))
	migrate(db, p.Contribs)

	p.LC.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			logrus.Info("Closing database connection")
			return sqlDB.Close()
		},
	})

	return db
}

func connectWithRetry(dbConfig *DatabaseConfig) *gorm.DB {
	const maxRetries = 3
	const retryDelay = 10 * time.Second

	dsn, err := dbConfig.ToDSN()
	if err != nil {
		logrus.Fatalf("Failed to construct DSN: %v", err)
	}

	for i := 0; i <= maxRetries; i++ {
		db, openErr := gorm.Open(mysql.Open(dsn), &gorm.Config{
			Logger: logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags),
				logger.Config{
					SlowThreshold:             time.Second,
					LogLevel:                  logger.Warn,
					IgnoreRecordNotFoundError: true,
					Colorful:                  true,
				}),
			TranslateError: true,
		})
		if openErr == nil {
			logrus.Info("Successfully connected to the database")
			if pluginErr := db.Use(tracing.NewPlugin()); pluginErr != nil {
				panic(pluginErr)
			}
			return db
		}

		err = openErr
		logrus.Errorf("Failed to connect to database (attempt %d/%d): %v", i+1, maxRetries+1, err)
		if i < maxRetries {
			logrus.Infof("Retrying in %v...", retryDelay)
			time.Sleep(retryDelay)
		}
	}

	logrus.Fatalf("Failed to connect to database after %d attempts: %v", maxRetries+1, err)
	return nil
}
