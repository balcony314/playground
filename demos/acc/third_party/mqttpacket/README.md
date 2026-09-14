# MQTTPacket（vendored）

来源：Eclipse Paho MQTT C 客户端的 MQTTPacket 组件（Ian Craggs 等），
许可证：Eclipse Public License v1.0 / Eclipse Distribution License v1.0
双许可，原文见各文件头。

相对上游的修改：增加 need_len/walk_len 出参以支持流式半包解析。
不做其他改动。

## 上游已知缺陷

为保持 verbatim 纪律，以下上游缺陷不在本目录内修复；本清单供将来升级
Paho 版本时对照参考。

1. `MQTTDeserializePublish.c` 的 `MQTTDeserialize_publish`：当 QoS>0 且
   topic 读完 后剩余报文仅 0/1 字节时，`readInt` 读取前无 `enddata`
   越界检查，`payloadlen` 可被解出 -1/-2。acc 侧已在 `handle_publish`
   以 `payloadlen < 0` 判定并关闭连接防护。
2. `MQTTSubscribeServer.c` 的 `MQTTDeserialize_subscribe` 忽略 `maxcount`
   形参，不做订阅条数上限校验。acc 侧以 `rem/3+1` 作为条目数上界
   预分配数组补偿。
