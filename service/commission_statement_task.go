package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

// 月度出账任务（契约 01-CONTRACT.md 附录 D2 冻结：subscription_reset_task 模式平移）。
//
// 调度语义：每分钟 tick；`day >= clamp(payoutDay)` 宽进——出账日当天及当月内任意时刻
// （进程重启/停机补跑/月中改配置）都自动补齐上期账单，无需「当日已跑」标记；
// 启动即跑一次覆盖「进程在出账日后才启动」的场景。
//
// 防重三层（契约 §5/D2 冻结，禁止改用 cron 库/分布式锁/system_task DB-lease）：
//  1. IsMasterNode 门（挡 slave 节点；多 master 合法并存，不能只靠这层）
//  2. uk_stmt_period(distributor_id, period) 唯一索引（DB 级最终防线）
//  3. 分销商 users 行 FOR UPDATE（事务内串行化，model 层）
//
// 门控纪律（附录 D5.1）：出账不受分佣总开关门控——纠错与对账是账本完整性操作，
// 开关只控制新增记账入口。
//
// 时区纪律（契约 C3 + Pitfall 8）：全程使用 now 自身 Location（服务器本地时区；
// 部署假设 TZ=Asia/Shanghai，docker-compose 已设置），勿混用 time.UTC。

var (
	statementTaskOnce    sync.Once
	statementTaskRunning atomic.Bool
)

// statementTaskTickInterval 出账判定 tick 周期（模式 A：每分钟一次 day-of-month 判定）。
const statementTaskTickInterval = time.Minute

// StartCommissionStatementTask 启动月度出账任务（仅 master 节点）。
// 注册点：main.go StartSubscriptionQuotaResetTask() 之后。
func StartCommissionStatementTask() {
	statementTaskOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("commission statement task started: node=%s tick=%s", common.NodeName, statementTaskTickInterval))
			// 启动即跑一次：覆盖「进程在出账日后才启动」的补跑场景（D2 宽进语义）
			runStatementGenerationOnce(time.Now())
			ticker := time.NewTicker(statementTaskTickInterval)
			defer ticker.Stop()
			for now := range ticker.C {
				runStatementGenerationOnce(now)
			}
		})
	})
}

// payoutDayClamped 短月 clamp（契约 §3.4：当月天数不足取最后一天）。
// 例：payoutDay=31 的 2 月（28/29 天）返回 28/29——否则 2 月出账永久跳过（Pitfall 2）；
// 抽出纯函数便于单测（不触碰 DB/时钟）。
func payoutDayClamped(now time.Time) int {
	lastDay := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	if common.CommissionPayoutDay > lastDay {
		return lastDay
	}
	return common.CommissionPayoutDay
}

// runStatementGenerationOnce 单次出账判定（参数化 now 供单测喂任意日期，
// 是 Pitfall 1/2/8 的测试接缝）。CAS 防重入：并发/重叠 tick 只有一个执行体生效。
func runStatementGenerationOnce(now time.Time) {
	if !statementTaskRunning.CompareAndSwap(false, true) {
		return
	}
	defer statementTaskRunning.Store(false)

	// 未到出账日（宽进语义：>= 即尝试；幂等由 model 层三重防重保证，重复执行天然 no-op）
	if now.Day() < payoutDayClamped(now) {
		return
	}

	n, err := model.GenerateDueStatements(now)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("commission statement generation failed: node=%s err=%v", common.NodeName, err))
		return
	}
	if n > 0 {
		logger.LogInfo(context.Background(), fmt.Sprintf("commission statements generated: node=%s period_count=%d", common.NodeName, n))
	}
}
