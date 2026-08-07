-- Actor 通用持久化表结构示例。
-- 框架启动时按类别 spec 自动 CREATE TABLE IF NOT EXISTS（见 pkg/storage MysqlSync.EnsureTable），
-- 此处为文档/手工建库参考。每类 actor 一张表，行结构统一：
--   actor_id  全局唯一 uid（Player 使用账号体系分配的玩家 id）
--   data      序列化后的数据字节（当前为 JSON，后续可切 protobuf）
--   version   版本号（预留，多实例 LWW 后续强化）
--   updated_at 最后更新时间

CREATE TABLE IF NOT EXISTS player_data (
    actor_id   BIGINT      NOT NULL,
    data       LONGBLOB    NOT NULL,
    version    BIGINT      NOT NULL DEFAULT 0,
    updated_at DATETIME(3) NOT NULL,
    PRIMARY KEY (actor_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4;

-- webapi 服务账号表（name -> uid 映射，uid 作为玩家持久化主键）
CREATE TABLE IF NOT EXISTS accounts (
    name       VARCHAR(64) NOT NULL,
    uid        BIGINT      NOT NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (name),
    UNIQUE KEY uk_uid (uid)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4;
