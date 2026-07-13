package database

import (
	"context"
	"fmt"
	"time"

	"defolt-tenants-service/config"
	"defolt-tenants-service/logger"
	"defolt-tenants-service/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func Connect(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger:      gormlogger.Default.LogMode(gormlogger.Warn),
		PrepareStmt: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, err
	}
	logger.LogInfo("db", fmt.Sprintf("connected to %s@%s:%s", cfg.DBName, cfg.DBHost, cfg.DBPort))
	return db, nil
}

func EnsureSchema(db *gorm.DB, cfg *config.Config) {
	if !cfg.AutoMigrate {
		return
	}
	if err := db.AutoMigrate(&model.Tenant{}); err != nil {
		logger.LogWarn("", "db-migrate", fmt.Sprintf("AutoMigrate Tenant: %v", err))
	}
}
