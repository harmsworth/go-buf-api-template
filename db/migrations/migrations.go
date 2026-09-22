// Package migrations 将 db/migrations 下的 .sql 迁移文件嵌入二进制，
// 供 golang-migrate 以 iofs 为 source 在启动时执行，避免部署时依赖外部目录。
package migrations

import "embed"

// FS 为嵌入的迁移文件集合，根目录即 db/migrations。
//
//go:embed *.sql
var FS embed.FS
