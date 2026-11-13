-- 宠物门店预约系统 · V1 初始 schema（F2.1）
-- 真相源：MySQL 8.0+；金额一律 BIGINT「分」；名额占用/释放走条件更新。
-- 约束锚点：.agent/backend/tech-stack.md §1.1、SRS 5.2 / 5.4 / 8 章。

CREATE TABLE IF NOT EXISTS slots (
    slot_id          VARCHAR(64)  PRIMARY KEY,
    store_id         VARCHAR(64)  NOT NULL,
    service_item_id  VARCHAR(64)  NOT NULL,
    start_at         DATETIME(6)  NOT NULL,
    end_at           DATETIME(6)  NOT NULL,
    capacity         INT          NOT NULL DEFAULT 3,
    occupied_count   INT          NOT NULL DEFAULT 0,
    CHECK (end_at > start_at),
    CHECK (occupied_count >= 0 AND occupied_count <= capacity)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS appointments (
    appointment_id     VARCHAR(64)  PRIMARY KEY,
    store_id           VARCHAR(64)  NOT NULL,
    slot_id            VARCHAR(64)  NOT NULL,
    user_id            VARCHAR(64)  NOT NULL,
    pet_id             VARCHAR(64)  NOT NULL,
    status             VARCHAR(32)  NOT NULL,
    amount_fen         BIGINT       NOT NULL,
    created_at         DATETIME(6)  NOT NULL,
    pay_deadline_at    DATETIME(6)  NOT NULL,
    version            BIGINT       NOT NULL DEFAULT 1,
    -- active=1 表示有效预约；终态置 NULL，多个 NULL 不触发唯一索引。
    active             TINYINT      NULL     DEFAULT 1,
    slot_released      TINYINT      NOT NULL DEFAULT 0,
    UNIQUE KEY uk_pet_slot_active (pet_id, slot_id, active),
    KEY idx_appointment_slot (slot_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS payments (
    payment_id         VARCHAR(64)  PRIMARY KEY,
    appointment_id     VARCHAR(64)  NOT NULL,
    amount_fen         BIGINT       NOT NULL,
    channel_trade_no   VARCHAR(128) NOT NULL,
    notify_key         VARCHAR(128) NOT NULL,
    status             VARCHAR(32)  NOT NULL,
    UNIQUE KEY uk_channel_trade_no (channel_trade_no),
    UNIQUE KEY uk_notify_key (notify_key)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS cancel_requests (
    request_id         VARCHAR(64)  PRIMARY KEY,
    appointment_id     VARCHAR(64)  NOT NULL,
    reason             TEXT         NOT NULL,
    apply_at           DATETIME(6)  NOT NULL,
    hours_to_start     DECIMAL(6,2) NOT NULL,
    type               VARCHAR(32)  NOT NULL,
    status             VARCHAR(32)  NOT NULL,
    reviewer           VARCHAR(64)  DEFAULT NULL,
    reviewed_at        DATETIME(6)  DEFAULT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS refunds (
    refund_id          VARCHAR(64)  PRIMARY KEY,
    appointment_id     VARCHAR(64)  NOT NULL,
    payment_id         VARCHAR(64)  NOT NULL,
    amount_fen         BIGINT       NOT NULL,
    status             VARCHAR(32)  NOT NULL,
    retry_count        INT          NOT NULL DEFAULT 0,
    last_error         TEXT         DEFAULT NULL,
    created_at         DATETIME(6)  NOT NULL,
    finished_at        DATETIME(6)  DEFAULT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS service_records (
    record_id          VARCHAR(64)  PRIMARY KEY,
    appointment_id     VARCHAR(64)  NOT NULL UNIQUE,
    verify_by          VARCHAR(64)  DEFAULT NULL,
    verify_at          DATETIME(6)  DEFAULT NULL,
    started_at         DATETIME(6)  DEFAULT NULL,
    finished_at        DATETIME(6)  DEFAULT NULL,
    revenue_fen        BIGINT       NOT NULL DEFAULT 0,
    no_show_reason     TEXT         DEFAULT NULL
) ENGINE=InnoDB;

-- 只增不改的事件流水，支撑并发冲突/越权/异常留痕与日清反查。
CREATE TABLE IF NOT EXISTS event_logs (
    event_id           VARCHAR(64)  PRIMARY KEY,
    appointment_id     VARCHAR(64)  NOT NULL,
    action             VARCHAR(64)  NOT NULL,
    from_status        VARCHAR(32)  DEFAULT NULL,
    to_status          VARCHAR(32)  DEFAULT NULL,
    actor_role         VARCHAR(32)  DEFAULT NULL,
    actor_store_id     VARCHAR(64)  DEFAULT NULL,
    detail             TEXT         DEFAULT NULL,
    created_at         DATETIME(6)  NOT NULL,
    KEY idx_event_created_at (created_at)
) ENGINE=InnoDB;
