package model

// Example model 教学模板：model 是纯 GORM 数据结构
//
//   - 字段加上 `gorm:"..."` tag 控制 DDL；JSON tag 控制对外契约。
//   - **不要**在 model 上挂带业务规则的方法（鉴权、状态机、外部调用都属于 service）。
//   - 新增字段后跑 cmd/migrate 把 AutoMigrate 同步到 DB；正式项目建议换成
//     SQL 文件 + golang-migrate / atlas（skeleton 阶段先 AutoMigrate 够用）。

import "time"

// Example 是示例 GORM 模型，用来串通 handler → service → repository → model
// 的调用链；新增业务模型时复制本结构改字段即可。
type Example struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"column:name;type:varchar(255);not null" json:"name"`
	CreatedAt time.Time `gorm:"column:created_at;type:timestamp;default:CURRENT_TIMESTAMP;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:timestamp;default:CURRENT_TIMESTAMP;not null" json:"updated_at"`
}

// TableName 显式声明 Example 对应的数据库表名 examples。GORM 默认会按
// snake_case + 复数推断（这里恰好一致），显式声明是为了让以后改 struct
// 名（如 Example → DemoEntity）时表名不会跟着漂。
func (Example) TableName() string {
	return "examples"
}
