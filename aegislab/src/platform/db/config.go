package db

import (
	"fmt"

	"aegis/platform/config"
)

type DatabaseConfig struct {
	Type     string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Timezone string
}

func NewDatabaseConfig(databaseType string) *DatabaseConfig {
	return &DatabaseConfig{
		Type:     databaseType,
		Host:     config.GetString(fmt.Sprintf("database.%s.host", databaseType)),
		Port:     config.GetInt(fmt.Sprintf("database.%s.port", databaseType)),
		User:     config.GetString(fmt.Sprintf("database.%s.user", databaseType)),
		Password: config.GetString(fmt.Sprintf("database.%s.password", databaseType)),
		Database: config.GetString(fmt.Sprintf("database.%s.db", databaseType)),
		Timezone: config.GetString(fmt.Sprintf("database.%s.timezone", databaseType)),
	}
}

func (d *DatabaseConfig) ToDSN() (string, error) {
	if d.Type != "mysql" {
		return "", fmt.Errorf("unsupported database type: %s", d.Type)
	}

	// Pin connection collation to match the schema's table collation
	// (utf8mb4_unicode_ci). Without this MySQL 8 defaults the connection
	// to utf8mb4_0900_ai_ci, which makes literal-vs-column comparisons
	// fail with "Illegal mix of collations". Symptom we hit: `inject
	// submit` 500s in mapPedestalContainerRefToVersion.
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=True&loc=Local",
		d.User, d.Password, d.Host, d.Port, d.Database), nil
}
