package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 本文件为 package model 白盒测试：updateOptionMap 未导出（option.go:262），
// 须与 option.go 同 package 才能直接调用。参照 task_cas_test.go 的 package 声明。
//
// model 包 TestMain 已由 task_cas_test.go 占用（内存库 + initCol + AutoMigrate），
// 本文件不新增 TestMain，复用共享 fixture（model.DB 由 TestMain 初始化）。

// TestCommissionOptions_Defaults verifies the 4 commission/aff 选项的 InitOptionMap
// 默认值注册 (SETT-01 热更新链路第一环, 契约 01-CONTRACT.md §2.4 + §3.4).
// 02-01 Task 3 已实现:
//   - common/option_vars.go: 4 全局变量默认值声明 (CommissionEnabled=false,
//     CommissionPayoutDay=8, AffRebindWindowMode="days", AffRebindWindowDays=7)
//   - model/option.go:179-183: InitOptionMap 注册 4 键到 OptionMap
//
// 本测试补齐 02-VALIDATION.md Per-Task Verification Map 的 SETT-01 Wave 0 缺口.
func TestCommissionOptions_Defaults(t *testing.T) {
	// 保存原值，测试后恢复（避免污染其他测试）
	origEnabled := common.CommissionEnabled
	origPayoutDay := common.CommissionPayoutDay
	origMode := common.AffRebindWindowMode
	origDays := common.AffRebindWindowDays
	origOptionMap := common.OptionMap
	t.Cleanup(func() {
		common.CommissionEnabled = origEnabled
		common.CommissionPayoutDay = origPayoutDay
		common.AffRebindWindowMode = origMode
		common.AffRebindWindowDays = origDays
		// 恢复 OptionMap：若原值为 nil，恢复为空 map 而非 nil，避免后续测试
		// 调 updateOptionMap 时 `OptionMap[key]=value` 在 nil map 上 panic。
		if origOptionMap == nil {
			common.OptionMapRWMutex.Lock()
			common.OptionMap = make(map[string]string)
			common.OptionMapRWMutex.Unlock()
			return
		}
		common.OptionMap = origOptionMap
	})

	// 重置全局变量到默认值（避免被先前测试污染，确保 InitOptionMap 注册的是默认值）
	common.CommissionEnabled = false
	common.CommissionPayoutDay = 8
	common.AffRebindWindowMode = "days"
	common.AffRebindWindowDays = 7

	// 确保 options 表存在（loadOptionsFromDatabase 调 AllOption 查询；
	// 表不存在时 Find 返回错误被忽略，但为避免日志噪音先 AutoMigrate）
	require.NoError(t, DB.AutoMigrate(&Option{}))

	// 调用 InitOptionMap 注册默认值（option.go:30-193）
	InitOptionMap()

	// 断言全局变量默认值（common/option_vars.go 声明处）
	require.Equal(t, false, common.CommissionEnabled, "CommissionEnabled default should be false")
	require.Equal(t, 8, common.CommissionPayoutDay, "CommissionPayoutDay default should be 8")
	require.Equal(t, "days", common.AffRebindWindowMode, "AffRebindWindowMode default should be 'days'")
	require.Equal(t, 7, common.AffRebindWindowDays, "AffRebindWindowDays default should be 7")

	// 断言 OptionMap 注册的默认值字符串（option.go:179-183）
	require.Equal(t, "false", common.OptionMap["CommissionEnabled"], "OptionMap CommissionEnabled default")
	require.Equal(t, "8", common.OptionMap["CommissionPayoutDay"], "OptionMap CommissionPayoutDay default")
	require.Equal(t, "days", common.OptionMap["AffRebindWindowMode"], "OptionMap AffRebindWindowMode default")
	require.Equal(t, "7", common.OptionMap["AffRebindWindowDays"], "OptionMap AffRebindWindowDays default")
}

// TestCommissionOptions_UpdateOptionMap verifies the 4 commission/aff 选项的
// updateOptionMap case 映射生效 (SETT-01 热更新链路第二环, 契约 F2).
// 02-01 Task 3 已实现 updateOptionMap 的 4 case:
//   - CommissionEnabled: bool case (option.go:373-374, 在 strings.HasSuffix("Enabled") 分支)
//   - CommissionPayoutDay: int case (option.go:584-586, switch 末尾)
//   - AffRebindWindowMode: string case (option.go:587-588)
//   - AffRebindWindowDays: int case (option.go:589-591)
//
// 本测试补齐 02-VALIDATION.md Per-Task Verification Map 的 SETT-01 Wave 0 缺口.
func TestCommissionOptions_UpdateOptionMap(t *testing.T) {
	// 保存原值，测试后恢复
	origEnabled := common.CommissionEnabled
	origPayoutDay := common.CommissionPayoutDay
	origMode := common.AffRebindWindowMode
	origDays := common.AffRebindWindowDays
	t.Cleanup(func() {
		common.CommissionEnabled = origEnabled
		common.CommissionPayoutDay = origPayoutDay
		common.AffRebindWindowMode = origMode
		common.AffRebindWindowDays = origDays
	})

	// 确保 OptionMap 非 nil：updateOptionMap (option.go:265) 执行 `OptionMap[key]=value`,
	// 若 OptionMap 为 nil 会 panic（"assignment to entry in nil map"）。
	// task_cas_test.go 的 TestMain 不初始化 OptionMap，故此处须自给自足。
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	// CommissionEnabled: bool case (option.go:373-374)
	// updateOptionMap 先 common.OptionMap[key]=value，再走 strings.HasSuffix("Enabled") 分支
	require.NoError(t, updateOptionMap("CommissionEnabled", "true"))
	require.Equal(t, true, common.CommissionEnabled, "updateOptionMap should set CommissionEnabled=true")
	require.Equal(t, "true", common.OptionMap["CommissionEnabled"], "OptionMap should reflect CommissionEnabled=true")

	// CommissionPayoutDay: int case (option.go:584-586, switch 末尾)
	require.NoError(t, updateOptionMap("CommissionPayoutDay", "15"))
	require.Equal(t, 15, common.CommissionPayoutDay, "updateOptionMap should set CommissionPayoutDay=15")
	require.Equal(t, "15", common.OptionMap["CommissionPayoutDay"], "OptionMap should reflect CommissionPayoutDay=15")

	// AffRebindWindowMode: string case (option.go:587-588)
	require.NoError(t, updateOptionMap("AffRebindWindowMode", "unlimited"))
	require.Equal(t, "unlimited", common.AffRebindWindowMode, "updateOptionMap should set AffRebindWindowMode=unlimited")
	require.Equal(t, "unlimited", common.OptionMap["AffRebindWindowMode"], "OptionMap should reflect AffRebindWindowMode=unlimited")

	// AffRebindWindowDays: int case (option.go:589-591)
	require.NoError(t, updateOptionMap("AffRebindWindowDays", "30"))
	require.Equal(t, 30, common.AffRebindWindowDays, "updateOptionMap should set AffRebindWindowDays=30")
	require.Equal(t, "30", common.OptionMap["AffRebindWindowDays"], "OptionMap should reflect AffRebindWindowDays=30")
}
