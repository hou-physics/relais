package cli

import (
	"context"

	"github.com/hou-physics/relais/internal/server"
)

// SaveGlobalForTest 供 e2e 测试注入身份；生产代码不得调用。
func SaveGlobalForTest(server, token, username string) error {
	return saveGlobal(&GlobalConfig{Server: server, Token: token, Username: username})
}

// PollOnceForTest 供 e2e 驱动一轮 bridge 轮询；生产代码不得调用。
func PollOnceForTest(c *Client, channel, dir string) (int, error) {
	return pollOnce(c, []bridgeTarget{{Channel: channel, Dir: dir}}, "", nil)
}

// NewClientForTest 暴露 newClient；生产代码不得调用。
func NewClientForTest() (*Client, error) {
	c, _, err := newClient()
	return c, err
}

// NewLocalManagerForTest 暴露本地管理器；生产代码不得调用。
func NewLocalManagerForTest(ld string) server.LocalManager { return newLocalManager(ld) }

// RunServeCtx 暴露可用 ctx 停止的 serve；生产代码不得调用。
func RunServeCtx(ctx context.Context, args []string) error { return runServe(ctx, args) }
