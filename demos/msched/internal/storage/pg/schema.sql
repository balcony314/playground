-- msched 存储层 DDL（CockroachDB，DESIGN §3）
-- 幂等（IF NOT EXISTS），ApplySchema 启动/测试时执行。

-- tasks：全量真相源。id 为 DB 物理主键（SERIAL8 = CRDB unique_rowid），
-- unit_id 为上层唯一标识（UNIQUE，= hash(group+Args)，DESIGN §3），CAS 定位走 unit_id。
CREATE TABLE IF NOT EXISTS tasks (
    id                SERIAL8 PRIMARY KEY,            -- DB 物理主键（CRDB unique_rowid，时间戳有序；上层用 unit_id）
    unit_id           TEXT NOT NULL UNIQUE,            -- 上层唯一标识 + CAS 定位键（= hash(group+Args)，DESIGN §3）
    group_uid          TEXT NOT NULL,                   -- 租户隔离 ID（多租户公平作用域）
    priority          BIGINT NOT NULL,                 -- 撮合 ORDER BY 排序键（默认=created_at 微秒）
    state             TEXT NOT NULL,                   -- PENDING/BACKOFF/SCHEDULED/RUNNING/COMPLETED/DISCARDED
    worker_selector   JSONB NOT NULL,                 -- WorkerSelector map（单向匹配约束 S_t ⊆ L_w）
    selector_hash     TEXT NOT NULL,                   -- = ComputeSelectorHash，group_stats 维度键
    max_exec_duration BIGINT NOT NULL,                 -- 执行时长上限（ns），RUNNING 硬截止依据
    args              BYTES NOT NULL,                  -- 任务参数（大字段，留 PG 不进 Redis）
    result            BYTES,                          -- 执行结果（COMPLETED 时写）
    dispatch_time     TIMESTAMPTZ,                     -- CAS PENDING->SCHEDULED 时设；派发超时判定依据
    pull_time         TIMESTAMPTZ,                     -- worker 拉走时设；执行硬截止起算点
    timeout           TIMESTAMPTZ,                     -- = pull_time + max_exec_duration，仅 RUNNING 生效
    next_retry_time   TIMESTAMPTZ,                     -- BACKOFF 退避下次可撮合时间（PENDING 时 NULL）
    fail_count        BIGINT NOT NULL DEFAULT 0,        -- 连续无匹配次数，决定退避时长
    dispatch_count    BIGINT NOT NULL DEFAULT 0,       -- 派发次数，超限转 DISCARDED
    lease_owner       TEXT NOT NULL DEFAULT '',        -- 派发租约持有 worker UID
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()  -- 修改时间，每次 UPDATE now() 维护（DESIGN §3）
);

-- 撮合查询复合索引（DESIGN §3.1）：定向扫描 WHERE group_uid=? AND (PENDING OR (BACKOFF AND next_retry_time<=now))
-- 10 亿全量不压单 range 索引。
CREATE INDEX IF NOT EXISTS idx_tasks_match ON tasks (group_uid, state, next_retry_time, priority);

-- workers：在线 worker 视图来源（DESIGN §3/§4.1）。id 主键，unit_id UNIQUE 上层标识。
CREATE TABLE IF NOT EXISTS workers (
    id                 SERIAL8 PRIMARY KEY,            -- DB 物理主键（CRDB unique_rowid）
    unit_id            TEXT NOT NULL UNIQUE,           -- 上层唯一标识（与 Task.UnitID 统一）
    labels             JSONB NOT NULL,                 -- 被 Task.WorkerSelector 子集匹配
    capacity           INT NOT NULL DEFAULT 0,         -- 并发槽位（容量模型待定，DESIGN §11）
    lease_expire_time  TIMESTAMPTZ NOT NULL,           -- 在线心跳 lease 到期时间
    state              TEXT NOT NULL DEFAULT '',
    token              TEXT NOT NULL DEFAULT '',        -- 身份校验凭证，RPC 入口比对
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- group_stats：(group_uid, selector_hash, state) 聚合任务计数（DESIGN §3），行式存储。
-- id 为 DB 物理主键（标准化，同 tasks/workers）；(group_uid, selector_hash, state) 为业务唯一键（UNIQUE），
-- UPSERT/UPDATE 定位走 UNIQUE（非 id）。created_at/updated_at 审计时间，count 增减时维护 updated_at。
CREATE TABLE IF NOT EXISTS group_stats (
    id              SERIAL8 PRIMARY KEY,              -- DB 物理主键（CRDB unique_rowid；标准化，同 tasks/workers）
    group_uid       TEXT NOT NULL,
    selector_hash   TEXT NOT NULL,
    selectors       TEXT[] NOT NULL DEFAULT '{}',      -- selector_hash 可读伴随（"k=v" 按 key 排序）
    state           TEXT NOT NULL,                     -- PENDING/BACKOFF/SCHEDULED/RUNNING/COMPLETED/DISCARDED
    count           BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(), -- 修改时间，count 增减时 now() 维护（DESIGN §3）
    UNIQUE (group_uid, selector_hash, state)           -- 业务唯一键，UPSERT/UPDATE 定位走此（非 id）
);
