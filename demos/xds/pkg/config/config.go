// Package config 封装 viper：按 CONF_ENV 选择 conf/<env>.yaml，
// 环境变量以 xds_ 前缀覆盖配置项（如 xds_nacos_port 覆盖 nacos.port）
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

var v = viper.New()

// Init 加载配置文件。configPath 非空时直接使用；
// 为空时按 CONF_ENV 环境变量（默认 dev）在 ./conf、../conf 中查找 <env>.yaml
func Init(configPath string) error {
	if configPath == "" {
		env := strings.ToLower(os.Getenv("CONF_ENV"))
		if env == "" {
			env = "dev"
		}
		name := fmt.Sprintf("%s.yaml", env)
		found, err := findConfigFile(name, "conf", "../conf")
		if err != nil {
			return fmt.Errorf("查找配置文件失败: %w", err)
		}
		configPath = found
	}
	fmt.Printf("starting with config: %s\n", configPath)

	v.SetConfigFile(configPath)
	v.AutomaticEnv()
	v.SetEnvPrefix("xds")
	// viper 内部会把 env 键转为全大写（XDS_NACOS.PORT），这里通过 replacer
	// 将大写折叠回小写并把 "." 替换为 "_"，使 AutomaticEnv 能命中约定的小写
	// 环境变量（nacos.port → xds_nacos_port）
	v.SetEnvKeyReplacer(envKeyReplacer())
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	return nil
}

// envKeyReplacer 构造环境变量键替换器：A-Z 折叠为 a-z、"." 替换为 "_"。
// viper 查找 env 时先拼出全大写键（XDS_NACOS.PORT），经此替换器还原为
// 小写下划线形式（xds_nacos_port），与 xds_ 前缀小写约定保持一致
func envKeyReplacer() *strings.Replacer {
	pairs := make([]string, 0, 26*2+2)
	for c := byte('A'); c <= 'Z'; c++ {
		pairs = append(pairs, string(c), string(c+('a'-'A')))
	}
	pairs = append(pairs, ".", "_")
	return strings.NewReplacer(pairs...)
}

func findConfigFile(name string, dirs ...string) (string, error) {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("file %s not found in %v", name, dirs)
}
