export const nexusScheduleZhCN = {
  "nexusSchedule.title": "周期迁移计划",
  "nexusSchedule.new": "创建周期计划",
  "nexusSchedule.refresh": "刷新计划",
  "nexusSchedule.background": "服务端持续管理计划",
  "nexusSchedule.backgroundHint":
    "每轮完整重枚举并创建持久增量批次，关闭浏览器不影响执行。错过的周期合并为一次；前一批次结束前不会新建批次。",
  "nexusSchedule.capacityBoundary": "每轮需要新的容量观测",
  "nexusSchedule.capacityBoundaryHint":
    "目前没有可信的远端对象池探测。每份管理员上报的容量观测只供一轮使用，缺失或过期时计划等待更新；这还不是完全无人值守迁移。",
  "nexusSchedule.empty": "还没有周期计划",
  "nexusSchedule.emptyHint":
    "先完成一次成功的迁移批次，再用它的源版本和仓库映射建立计划。",
  "nexusSchedule.template": "成功批次",
  "nexusSchedule.templateHint":
    "计划固定该批次的源版本、映射和策略。更改这些配置后需要重新完成批次并建立新计划。",
  "nexusSchedule.interval": "周期（分钟）",
  "nexusSchedule.createHint":
    "创建后先等待新的容量观测。补充观测后，已到期计划会自动执行一次。",
  "nexusSchedule.active": "计划已启用",
  "nexusSchedule.waiting_capacity": "等待新容量观测",
  "nexusSchedule.paused": "计划已暂停",
  "nexusSchedule.blocked": "计划需要处理",
  "nexusSchedule.stopped": "计划已停止",
  "nexusSchedule.nextRun": "下次可触发时间",
  "nexusSchedule.lastSuccessful": "最近成功追平",
  "nexusSchedule.successScope":
    "仅表示本计划支持范围内的原生内容验证成功。原生重生成的 Maven 元数据仍保留在原批次明细中，不代表整个源已清零或客户端切换已验收。",
  "nexusSchedule.never": "尚无成功追平",
  "nexusSchedule.noNext": "已停止，不再触发",
  "nexusSchedule.intervalValue": "每 {minutes} 分钟",
  "nexusSchedule.currentBatch": "查看当前批次",
  "nexusSchedule.lastBatch": "查看最近批次",
  "nexusSchedule.pause": "暂停计划",
  "nexusSchedule.resume": "更新容量并恢复",
  "nexusSchedule.capacity": "补充容量观测",
  "nexusSchedule.stop": "停止计划",
  "nexusSchedule.stopConfirm":
    "停止后不再创建新批次，且不能恢复此计划。正在运行的批次和已发布内容会保留。",
  "nexusSchedule.stopHint":
    "暂停或停止只影响未来批次；当前批次请在批次页面单独管理。",
  "nexusSchedule.saveCapacity": "提交新观测",
  "nexusSchedule.blockedHint":
    "请检查最近批次；若源版本或映射已改变，请基于新的成功批次建立计划。",
  "nexusSchedule.mappingBusy":
    "同一目标已有活跃批次，计划已阻断。请先处理该批次，再显式恢复计划。",
  "nexusSchedule.mappingExists":
    "这些目标已有未停止的周期计划，请管理已有计划。",
  "nexusSchedule.templateRequired":
    "请选择完整成功且没有阻断异常的已完成批次。",
  "nexusSchedule.dispatchFailed":
    "计划提交遇到错误，最多自动尝试三次；达到上限后需要处理。",
  "nexusSchedule.policyChanged": "计划策略已变化，请基于新的成功批次建立计划。",
} as const;
export type NexusScheduleMessageKey = keyof typeof nexusScheduleZhCN;
