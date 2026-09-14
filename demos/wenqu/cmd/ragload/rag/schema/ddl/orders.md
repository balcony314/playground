-- 订单主表：电商交易订单，按月分区，订单号为查询主键
CREATE TABLE demo.orders
(
    `order_id`      String COMMENT '订单号，业务主键',
    `user_id`       UInt32 COMMENT '用户 ID',
    `product_id`    UInt32 COMMENT '商品 ID',
    `category`      LowCardinality(String) COMMENT '商品类目：electronics/clothing/food/home/sports',
    `amount`        Decimal(18, 2) COMMENT '订单金额（元）',
    `quantity`      UInt16 COMMENT '购买数量',
    `pay_status`    Enum8('pending' = 1, 'paid' = 2, 'refunded' = 3, 'cancelled' = 4) COMMENT '支付状态',
    `channel`       LowCardinality(String) COMMENT '下单渠道：app/web/miniapp',
    `province`      LowCardinality(String) COMMENT '收货省份',
    `created_at`    DateTime COMMENT '下单时间',
    `updated_at`    DateTime COMMENT '状态更新时间'
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (order_id, created_at)
COMMENT '电商订单明细表';
