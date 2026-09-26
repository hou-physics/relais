package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// sessions.toml：每（频道）一条讨论脑会话 id（本侧配置目录下，D36）。
type sessionEntry struct {
	SessionID string `toml:"session_id"`
	CreatedAt string `toml:"created_at"`
}

type sessionRegistry struct {
	Channels map[string]sessionEntry `toml:"channels"`
}

func sessionsPath(dir string) string { return filepath.Join(dir, "sessions.toml") }

func loadSessions(dir string) (*sessionRegistry, error) {
	reg := &sessionRegistry{Channels: map[string]sessionEntry{}}
	if _, err := toml.DecodeFile(sessionsPath(dir), reg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if reg.Channels == nil {
		reg.Channels = map[string]sessionEntry{}
	}
	return reg, nil
}

func saveSessions(dir string, reg *sessionRegistry) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(sessionsPath(dir), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(reg)
}

func sessionGet(dir, channel string) (string, error) {
	reg, err := loadSessions(dir)
	if err != nil {
		return "", err
	}
	return reg.Channels[channel].SessionID, nil
}

func sessionSet(dir, channel, id string) error {
	reg, err := loadSessions(dir)
	if err != nil {
		return err
	}
	reg.Channels[channel] = sessionEntry{SessionID: id, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	return saveSessions(dir, reg)
}

func sessionClear(dir, channel string) error {
	reg, err := loadSessions(dir)
	if err != nil {
		return err
	}
	delete(reg.Channels, channel)
	return saveSessions(dir, reg)
}

// RunSession 供 hook 调：relais session get | set <id> | clear（频道 = 当前项目绑定的频道）。
func RunSession(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais session <get|set <id>|clear>")
	}
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "get":
		id, err := sessionGet(dir, proj.Channel)
		if err != nil {
			return err
		}
		fmt.Print(id)
		return nil
	case "set":
		if len(args) != 2 || args[1] == "" {
			return fmt.Errorf("用法: relais session set <id>")
		}
		return sessionSet(dir, proj.Channel, args[1])
	case "clear":
		return sessionClear(dir, proj.Channel)
	default:
		return fmt.Errorf("未知 session 子命令 %q", args[0])
	}
}
