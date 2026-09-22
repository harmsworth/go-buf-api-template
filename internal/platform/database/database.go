// Package database 负责 GORM 初始化、连接池配置与 golang-migrate 迁移执行。
package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-buf-api-template/db/migrations"
	"go-buf-api-template/internal/conf"

	sqlmysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// New 建立 GORM 连接并配置连接池。
// 注意：本项目禁用 GORM AutoMigrate，表结构一律由 db/migrations 下的 SQL 迁移文件维护。
func New(cfg *conf.Database, log *slog.Logger) (*gorm.DB, error) {
	if cfg.GetDsn() == "" {
		return nil, errors.New("database.dsn is empty")
	}

	level := gormlogger.Warn
	if cfg.GetDebug() {
		level = gormlogger.Info
	}

	db, err := gorm.Open(mysql.Open(cfg.GetDsn()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(level),
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}

	maxOpen := int(cfg.GetMaxOpenConns())
	if maxOpen <= 0 {
		maxOpen = 50
	}
	maxIdle := int(cfg.GetMaxIdleConns())
	if maxIdle <= 0 {
		maxIdle = 10
	}
	lifetime := time.Duration(cfg.GetConnMaxLifetime()) * time.Second
	if lifetime <= 0 {
		lifetime = time.Hour
	}

	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(lifetime)

	log.Info("database connected", "driver", cfg.GetDriver(), "max_open", maxOpen, "max_idle", maxIdle)
	return db, nil
}

// EnsureDatabase 确保 DSN 中指定的 database 存在，不存在则创建。
// GORM 与 golang-migrate 都不会自动建库，这一步让服务对空实例也能开箱启动。
func EnsureDatabase(dsn string) error {
	cfg, err := sqlmysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	name := cfg.DBName
	if name == "" {
		return nil
	}

	// 先不带库名连上实例。
	cfg.DBName = ""
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("connect mysql server: %w", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS `" + name + "` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		return fmt.Errorf("create database %q: %w", name, err)
	}
	return nil
}

// Migrate 使用嵌入的 SQL 迁移文件将数据库升级到最新版本。
// 幂等：已执行过的版本不会重复执行（golang-migrate 返回 ErrNoChange 时视为成功）。
func Migrate(db *gorm.DB, log *slog.Logger) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB: %w", err)
	}
	return MigrateWith(sqlDB, log)
}

// MigrateWith 基于已有 *sql.DB 执行迁移，便于在测试或非 GORM 场景下复用。
func MigrateWith(sqlDB *sql.DB, log *slog.Logger) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load migration source: %w", err)
	}

	driver, err := migratemysql.WithInstance(sqlDB, &migratemysql.Config{})
	if err != nil {
		return fmt.Errorf("create migration database driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "mysql", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info("database schema is up to date, no migration applied")
			return nil
		}
		return fmt.Errorf("run migration: %w", err)
	}

	version, _, _ := m.Version()
	log.Info("database migration applied", "version", version)
	return nil
}
