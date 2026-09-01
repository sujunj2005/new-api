package model

import (
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// RecordCommissionTx 佣金记账引擎（契约 01-CONTRACT.md §3.5 冻结签名与调用时机）。
//
// 事务纪律（Pitfall 2）：全程只用传入 tx 查询/写入（tx.Select / tx.First / tx.Create），
// 禁用 model/user.go 内部走全局 DB 的用户查询助手（按邀请码取用户 Id、按 Id 取用户）——
// 其在事务外连接执行：主事务回滚时产生半提交幻觉，且与 MaxOpenConns(1) 测试库叠加必死锁。
//
// 门控语义（Pitfall 3）：门控分支（开关关/客户不存在/无归属/归属者非分销商/无比例配置）
// 一律 return nil——门控误返回 error 会把「无需记账」放大成「整个充值事务回滚」
// （用户已付款但额度不到账）；只有流水写库的真实 DB 错误才返回 error。
//
// 幂等（契约 §5 冻结）：uk_flow_billing(billing_no, flow_type) 唯一索引兜底，
// 拒绝任何分布式锁/Redis 锁/「查重-再插」两步逻辑。
func RecordCommissionTx(tx *gorm.DB, topUp *TopUp) error {
	// 1. 总开关门控（契约 §3.4，热更新链路见 common/option_vars.go）
	if !common.CommissionEnabled {
		return nil
	}
	// 2. 客户归属：直查 inviter_id（attribution.go:66-71 模式平移，全程只用 tx）
	var customer User
	if err := tx.Select("id, inviter_id").First(&customer, "id = ?", topUp.UserId).Error; err != nil {
		return nil // 客户不存在（异常数据）→ 不记账不阻断
	}
	if customer.InviterId == 0 {
		return nil // 无归属（ATTR-03）
	}
	// 3. 归属者须为分销商：精确 == 判定，禁阈值式 >=5（否则 admin=10/root=100 触发记账分支，T-03-01-02）
	var inviter User
	if err := tx.Select("id, role").First(&inviter, "id = ?", customer.InviterId).Error; err != nil {
		return nil
	}
	if inviter.Role != common.RoleDistributorUser {
		return nil // 普通邀请照旧，零佣金流水（ATTR-04）
	}
	// 4. 比例快照：无配置 = 未启用，直接返回 nil 不产生流水（契约 §7#3 冻结，非 0 金额流水）
	var rate CommissionRate
	if err := tx.Select("id, distributor_id, rate_bp").First(&rate, "distributor_id = ?", inviter.Id).Error; err != nil {
		return nil
	}
	// 5. 金额整数化：float→int 一次性 math.Round 转换并快照（契约 §3.0；
	//    佣金基数 = TopUp.Money 实付金额（F4），此后全链路 int64，禁 float 参与）
	moneyCents := int64(math.Round(topUp.Money * 100))
	flow := &CommissionFlow{
		BillingNo:       topUp.TradeNo, // 幂等键（契约 §3.1.1：commission 取 TradeNo）
		FlowType:        CommissionFlowCommission,
		TradeNo:         topUp.TradeNo,
		CustomerId:      customer.Id,
		DistributorId:   inviter.Id,
		TopupMoneyCents: moneyCents,
		RateBp:          rate.RateBp,
		AmountCents:     moneyCents * int64(rate.RateBp) / 10000, // int64 整除向下取整，尾差留平台侧（契约 §3.0）
		Status:          CommissionFlowPending,                   // Phase 4 出账时翻转为 available
		// 账期按服务器本地时区自然月（契约 C3，与 checkin.go:24 先例一致；
		// 运维须保证 TZ 配置稳定，docker-compose 已设 TZ=Asia/Shanghai）。
		// 不做 CompleteTime==0 fallback——契约 C2 已裁决由 EpayNotify 源头补写。
		Period: time.Unix(topUp.CompleteTime, 0).Format("2006-01"),
	}
	// 6. 写流水：uk_flow_billing(billing_no, flow_type) 唯一索引兜底幂等（契约 §5 冻结）
	if err := tx.Create(flow).Error; err != nil {
		return err // 只有流水写库的真实 DB 错误才返回 error（重放穿透时即唯一索引冲突）
	}
	return nil
}
