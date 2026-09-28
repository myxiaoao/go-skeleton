package model

// Example model 教学模板：model 是纯数据结构（应用层读写用）。
//
//   - JSON tag 控制对外契约；repository 负责把 sqlc 生成的行类型映射成这里
//     的字段。
//   - **不要**在 model 上挂带业务规则的方法（鉴权、状态机、外部调用都属于
//     service）。
//   - DDL 的真相源是 migrations/ 下的版本化 SQL，不是这个 struct——改表
//     结构走 "写 migrations/*.sql + 跑 make run-migrate"，struct 与迁移文件
//     需手动保持一致。

import "time"

// Example 是示例模型，用来串通 handler → service → repository → model
// 的调用链；新增业务模型时复制本结构改字段即可。
type Example struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
