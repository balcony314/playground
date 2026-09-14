-- 演示数据初始化：demo 库 + orders / user_visits 表（DDL 与 cmd/ragload/rag/schema/ddl 一致）
-- 幂等策略：先删后建，重复执行会重置为初始数据集
DROP DATABASE IF EXISTS demo;

CREATE DATABASE demo;

CREATE TABLE demo.orders
(
    `order_id`   String COMMENT '订单号，业务主键',
    `user_id`    UInt32 COMMENT '用户 ID',
    `product_id` UInt32 COMMENT '商品 ID',
    `category`   LowCardinality(String) COMMENT '商品类目：electronics/clothing/food/home/sports',
    `amount`     Decimal(18, 2) COMMENT '订单金额（元）',
    `quantity`   UInt16 COMMENT '购买数量',
    `pay_status` Enum8('pending' = 1, 'paid' = 2, 'refunded' = 3, 'cancelled' = 4) COMMENT '支付状态',
    `channel`    LowCardinality(String) COMMENT '下单渠道：app/web/miniapp',
    `province`   LowCardinality(String) COMMENT '收货省份',
    `created_at` DateTime COMMENT '下单时间',
    `updated_at` DateTime COMMENT '状态更新时间'
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (order_id, created_at)
COMMENT '电商订单明细表';

CREATE TABLE demo.user_visits
(
    `visit_id`    UInt64 COMMENT '访问流水 ID',
    `user_id`     UInt32 COMMENT '用户 ID，0 表示未登录',
    `session_id`  String COMMENT '会话 ID',
    `page`        LowCardinality(String) COMMENT '页面路径：/home /search /detail /cart /order',
    `referer`     String COMMENT '来源页',
    `device`      Enum8('mobile' = 1, 'pc' = 2, 'tablet' = 3) COMMENT '设备类型',
    `duration_ms` UInt32 COMMENT '停留时长（毫秒）',
    `is_bounce`   UInt8 COMMENT '是否跳出：1 是 / 0 否',
    `event_date`  Date COMMENT '事件日期',
    `created_at`  DateTime COMMENT '埋点上报时间'
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(event_date)
ORDER BY (visit_id, event_date)
TTL event_date + INTERVAL 180 DAY
COMMENT '用户访问行为流水表';

-- 订单数据：覆盖 2026-07 ~ 2026-09，多类目/状态/渠道/省份（2026-08 电子产品销售额最高，便于验证"上个月销售额最高的类目"）
INSERT INTO demo.orders
VALUES
    ('o20260701001', 1001, 501, 'electronics', 4999.00, 1, 'paid',      'app',    '广东', '2026-07-01 10:00:00', '2026-07-01 10:05:00'),
    ('o20260702002', 1002, 502, 'clothing',    299.50,  2, 'paid',      'web',    '北京', '2026-07-02 11:00:00', '2026-07-02 11:10:00'),
    ('o20260703003', 1003, 503, 'food',         59.90,  3, 'cancelled', 'miniapp','上海', '2026-07-03 12:00:00', '2026-07-03 12:30:00'),
    ('o20260715004', 1004, 504, 'home',        899.00,  1, 'paid',      'app',    '浙江', '2026-07-15 09:00:00', '2026-07-15 09:02:00'),
    ('o20260728005', 1005, 505, 'sports',      459.00,  2, 'pending',   'web',    '四川', '2026-07-28 16:00:00', '2026-07-28 16:00:00'),
    ('o20260801006', 1006, 506, 'electronics', 6999.00, 1, 'paid',      'app',    '广东', '2026-08-01 10:30:00', '2026-08-01 10:35:00'),
    ('o20260805007', 1007, 507, 'electronics', 12999.00, 1, 'paid',     'web',    '北京', '2026-08-05 14:00:00', '2026-08-05 14:03:00'),
    ('o20260810008', 1008, 508, 'clothing',    1299.00,  1, 'refunded', 'app',    '上海', '2026-08-10 20:00:00', '2026-08-12 09:00:00'),
    ('o20260812009', 1009, 509, 'electronics', 899.00,   2, 'paid',     'miniapp','江苏', '2026-08-12 08:00:00', '2026-08-12 08:06:00'),
    ('o20260815010', 1010, 510, 'food',        129.50,   5, 'paid',      'app',    '广东', '2026-08-15 12:00:00', '2026-08-15 12:01:00'),
    ('o20260820011', 1011, 511, 'home',        1599.00,  1, 'paid',      'web',    '浙江', '2026-08-20 15:00:00', '2026-08-20 15:05:00'),
    ('o20260825012', 1012, 512, 'electronics', 5999.00,  1, 'paid',      'app',    '山东', '2026-08-25 11:00:00', '2026-08-25 11:02:00'),
    ('o20260901013', 1013, 513, 'sports',      699.00,   1, 'paid',      'app',    '福建', '2026-09-01 09:00:00', '2026-09-01 09:01:00'),
    ('o20260905014', 1014, 514, 'electronics', 3299.00,  1, 'pending',  'web',    '广东', '2026-09-05 17:00:00', '2026-09-05 17:00:00'),
    ('o20260910015', 1015, 515, 'clothing',    399.00,   3, 'paid',      'miniapp','湖南', '2026-09-10 13:00:00', '2026-09-10 13:02:00');

-- 访问行为数据：2026-09 上旬，多页面/设备
INSERT INTO demo.user_visits
VALUES
    (900001, 1001, 's-a01', '/home',   'NULL',     1, 45000, 0, '2026-09-01', '2026-09-01 08:00:05'),
    (900002, 1002, 's-a02', '/search', '/home',    1, 30000, 0, '2026-09-01', '2026-09-01 08:05:10'),
    (900003, 1003, 's-a03', '/detail', '/search',  2, 60000, 0, '2026-09-02', '2026-09-02 10:10:00'),
    (900004, 0,    's-a04', '/home',   'NULL',     1,  3000, 1, '2026-09-03', '2026-09-03 12:00:00'),
    (900005, 1004, 's-a05', '/cart',   '/detail',  1, 20000, 0, '2026-09-04', '2026-09-04 14:20:00'),
    (900006, 1005, 's-a06', '/order',  '/cart',    3, 50000, 0, '2026-09-05', '2026-09-05 16:30:00'),
    (900007, 1006, 's-a07', '/detail', '/search',  1, 90000, 0, '2026-09-08', '2026-09-08 09:15:00'),
    (900008, 1007, 's-a08', '/home',   'NULL',     2,  5000, 1, '2026-09-10', '2026-09-10 11:00:00'),
    (900009, 1008, 's-a09', '/search', '/home',    1, 25000, 0, '2026-09-12', '2026-09-12 19:45:00'),
    (900010, 1009, 's-a10', '/detail', '/search',  1, 75000, 0, '2026-09-13', '2026-09-13 07:30:00');
