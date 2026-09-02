package model

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 超管人工补录/冲销流水（契约 01-CONTRACT.md §4.3 A7 + §3.1.1 + 附录 D1/D5.1，COMM-05）。
//
// 幂等键：BillingNo = "MAN-" 前缀 + 14 位时间戳 + 4 位随机字母数字（§3.1.1 冻结格式，
// manual 类型无 trade_no，uk_flow_billing(billing_no, flow_type) 天然唯一）。
//
// 落库语义（附录 D1 冻结缺省）：manual_debit 请求侧金额恒正、落库取负；
// TradeNo 空、CustomerId=0、TopupMoneyCents=0、RateBp=0；
// Period = 记录时刻当前月 → 被下期出账 sweep 自动纳入（D1「下期账单纳入」机制，
// 补录历史账期属契约增补，不在本期）。
//
// 门控语义（附录 D5.1 冻结）：调账入口不受分佣总开关门控——纠错是账本完整性操作，
// 开关只控制新增记账入口；本函数零门控分支。

// CreateManualFlow 超管人工补录（manual_credit）或冲销（manual_debit）一条佣金流水。
// 校验链 fail-fast 顺序平移 A2 先例：flow_type 白名单 → 金额恒正 → reason 必填/列宽 →
// 事务内目标存在且角色精确 == 分销商（禁阈值式——admin/root 不能被补录）。
func CreateManualFlow(distributorId int, flowType string, amountCents int64, reason string, operatorId int) error {
	if flowType != CommissionFlowManualCredit && flowType != CommissionFlowManualDebit {
		return fmt.Errorf("无效的流水类型 %q", flowType)
	}
	if amountCents <= 0 {
		// manual_debit 由落库取负，请求侧恒正（契约 A7）
		return fmt.Errorf("金额必须为正数")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("原因必填")
	}
	if utf8.RuneCountInString(reason) > 255 {
		return fmt.Errorf("原因长度不能超过 255 字符")
	}

	signed := amountCents
	if flowType == CommissionFlowManualDebit {
		signed = -amountCents // 冲销落库取负（契约 A7/D1）
	}
	billingNo := fmt.Sprintf("MAN-%s-%s", time.Now().Format("20060102150405"), common.GetRandomString(4))
	return DB.Transaction(func(tx *gorm.DB) error {
		var dist User
		if err := tx.Select("id, role").First(&dist, "id = ?", distributorId).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("目标分销商不存在")
			}
			return err
		}
		if dist.Role != common.RoleDistributorUser { // 精确 ==（A2 先例，禁阈值式）
			return fmt.Errorf("目标用户不是分销商")
		}
		return tx.Create(&CommissionFlow{
			BillingNo:       billingNo,
			FlowType:        flowType,
			TradeNo:         "", // 人工流水无充值单（D1）
			CustomerId:      0,
			DistributorId:   distributorId,
			TopupMoneyCents: 0,
			RateBp:          0,
			AmountCents:     signed,
			Status:          CommissionFlowPending, // 下期出账 sweep 消费
			Period:          time.Now().Format("2006-01"), // 当前月 → 下期账单自动纳入（D1）
			Reason:          reason,
			OperatorId:      operatorId,
		}).Error
	})
}
