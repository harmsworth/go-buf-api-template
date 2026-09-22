// Package config 负责加载应用启动配置。
//
// 约定：配置结构由 internal/conf/conf.proto 生成（*conf.Bootstrap），
// 通过 os.ReadFile + YAML→JSON + protojson 解析，**禁用 Viper**。
package config

import (
	"fmt"
	"os"

	"go-buf-api-template/internal/conf"

	"google.golang.org/protobuf/encoding/protojson"
	"sigs.k8s.io/yaml"
)

// Load 从 path 读取 YAML 配置并解析为 conf.Bootstrap。
func Load(path string) (*conf.Bootstrap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	// protojson 只吃 JSON，先把 YAML 转成 JSON。
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("convert yaml to json in %q: %w", path, err)
	}

	bs := &conf.Bootstrap{}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(jsonData, bs); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}
	return bs, nil
}
