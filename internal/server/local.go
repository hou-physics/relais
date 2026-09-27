package server

import "errors"

// ErrLocalInvalid：本地管理接口的"输入无效"哨兵，handler 据此回 400 而非 500。
var ErrLocalInvalid = errors.New("本地管理：输入无效")
