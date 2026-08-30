package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type GlobalConfig struct {
	Server   string `toml:"server"`
	Token    string `toml:"token"`
	Username string `toml:"username"`
}

func configDir() (string, error) {
	if d := os.Getenv("RELAIS_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "relais"), nil
}

func loadGlobal() (*GlobalConfig, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	var cfg GlobalConfig
	if _, err := toml.DecodeFile(filepath.Join(dir, "config.toml"), &cfg); err != nil {
		return nil, fmt.Errorf("尚未登录：请先运行 relais login <服务器地址> --token <你的token>")
	}
	return &cfg, nil
}

func saveGlobal(cfg *GlobalConfig) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "config.toml"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

type ProjectConfig struct {
	Server  string `toml:"server"`
	Channel string `toml:"channel"`
}

type ProjectBinding struct {
	Channel string `toml:"channel"`
	Dir     string `toml:"dir"`
}

type ProjectRegistry struct {
	Projects []ProjectBinding `toml:"projects"`
}

func registerProject(channel, dir string) error {
	registryDir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(registryDir, "projects.toml")
	ps, _ := loadProjects() // 已有记录
	// upsert：同 channel 覆盖 dir
	found := false
	for i, p := range ps {
		if p.Channel == channel {
			ps[i].Dir = dir
			found = true
			break
		}
	}
	if !found {
		ps = append(ps, ProjectBinding{Channel: channel, Dir: dir})
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(ProjectRegistry{Projects: ps})
}

func loadProjects() ([]ProjectBinding, error) {
	registryDir, err := configDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(registryDir, "projects.toml")
	var reg ProjectRegistry
	if _, err := toml.DecodeFile(path, &reg); err != nil {
		// 文件不存在 → 空表，不报错
		if os.IsNotExist(err) {
			return []ProjectBinding{}, nil
		}
		return nil, err
	}
	return reg.Projects, nil
}

func findProject() (string, *ProjectConfig, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	for {
		path := filepath.Join(dir, "relais", "config.toml")
		if _, err := os.Stat(path); err == nil {
			var cfg ProjectConfig
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return "", nil, fmt.Errorf("项目配置 %s 解析失败: %w", path, err)
			}
			return dir, &cfg, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil, fmt.Errorf("当前目录不在任何 Relais 项目内：请先在项目根目录运行 relais init <频道名>")
		}
		dir = parent
	}
}
