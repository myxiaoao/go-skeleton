package validator

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

// InitValidator 是注册自定义 binding 校验规则的钩子。当前空实现——遇到要
// 写跨字段校验（password != username 这种）时，在这里调
// binding.Validator.Engine().(*validator.Validate).RegisterValidation。
func InitValidator() {
}

// HandleValidatorError 把 validator.ValidationErrors 翻译成一条简洁、能直
// 接展示给客户端的 message。**只取第一条错误**，避免一次 binding 失败甩出
// 一堆字段名让前端难以展示；前端按 reason=INVALID_PARAMS 处理总分支即可。
func HandleValidatorError(errs validator.ValidationErrors) string {
	if len(errs) == 0 {
		return "invalid request parameters"
	}

	err := errs[0]
	field := strings.ToLower(err.Field())
	switch err.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "min":
		return fmt.Sprintf("%s must be at least %s", field, err.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", field, err.Param())
	default:
		return fmt.Sprintf("%s is invalid", field)
	}
}
