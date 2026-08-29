package cli

// SaveGlobalForTest 供 e2e 测试注入身份；生产代码不得调用。
func SaveGlobalForTest(server, token, username string) error {
	return saveGlobal(&GlobalConfig{Server: server, Token: token, Username: username})
}
