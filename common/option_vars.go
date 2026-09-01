package common

// 佣金与归属设置键全局变量（契约 01-CONTRACT.md §2.4 + §3.4 冻结）。
// 与 model/option.go InitOptionMap 默认值注册 + updateOptionMap case 构成热更新三件套（契约 F2）。
// 默认值在声明处给定（参照 constants.go FileUploadPermission 模式），
// 确保 OptionMap 与全局变量在首次启动（DB 无行）时一致。
var (
	CommissionEnabled   bool
	CommissionPayoutDay = 8
	AffRebindWindowMode = "days"
	AffRebindWindowDays = 7
)
