-- 宠物门店预约系统 · V2 schema（F6.5）：业务单据落库支撑表。
-- 与 001 一致：全部 IF NOT EXISTS，多次执行幂等，不破坏既有数据。
-- 约束锚点：.agent/backend/tech-stack.md §1.1（业务记录以 MySQL 为真相源）。

-- 退款状态变化流水（失败→重试→成功可查状态链，FR-10）。
CREATE TABLE IF NOT EXISTS refund_events (
    event_id       VARCHAR(64)  PRIMARY KEY,
    refund_id      VARCHAR(64)  NOT NULL,
    appointment_id VARCHAR(64)  NOT NULL,
    status         VARCHAR(32)  NOT NULL,
    retry_count    INT          NOT NULL DEFAULT 0,
    last_error     TEXT         DEFAULT NULL,
    created_at     DATETIME(6)  NOT NULL,
    KEY idx_re_appointment (appointment_id)
) ENGINE=InnoDB;

-- 异常登记（关闭后迟到回调等，SRS 13 章 A8）。
CREATE TABLE IF NOT EXISTS exceptions (
    exception_id   VARCHAR(64)  PRIMARY KEY,
    appointment_id VARCHAR(64)  NOT NULL,
    action         VARCHAR(64)  NOT NULL,
    detail         TEXT         DEFAULT NULL,
    created_at     DATETIME(6)  NOT NULL,
    KEY idx_ex_appointment (appointment_id)
) ENGINE=InnoDB;
