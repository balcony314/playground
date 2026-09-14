-- 用户访问行为表：页面浏览/点击流，按天分区，支持高频写入与时间范围聚合
CREATE TABLE demo.user_visits
(
    `visit_id`   UInt64 COMMENT '访问流水 ID',
    `user_id`    UInt32 COMMENT '用户 ID，0 表示未登录',
    `session_id` String COMMENT '会话 ID',
    `page`       LowCardinality(String) COMMENT '页面路径：/home /search /detail /cart /order',
    `referer`    String COMMENT '来源页',
    `device`     Enum8('mobile' = 1, 'pc' = 2, 'tablet' = 3) COMMENT '设备类型',
    `duration_ms` UInt32 COMMENT '停留时长（毫秒）',
    `is_bounce`  UInt8 COMMENT '是否跳出：1 是 / 0 否',
    `event_date` Date COMMENT '事件日期',
    `created_at` DateTime COMMENT '埋点上报时间'
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(event_date)
ORDER BY (visit_id, event_date)
TTL event_date + INTERVAL 180 DAY
COMMENT '用户访问行为流水表';
