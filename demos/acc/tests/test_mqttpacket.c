/* MQTTPacket（vendored）round-trip 测试 */
#include "acc_test.h"
#include "MQTTPacket.h"
#include "MQTTConnect.h"
#include "MQTTPublish.h"
#include <string.h>

/*
 * 注：raw 魔改点在 Deserialize 一侧 —— MQTTPacket_decode 保持上游
 * getcharfn 签名不变；need_len/walk_len 出参挂在
 * MQTTDeserialize_connect / publish / ack / connack / suback 等
 * 函数上。半包时返回 -1 且 *need_len 为整包所需长度；完整解析成功时
 * *walk_len == *need_len == 整包长度。
 */

ACC_TEST(connect_roundtrip)
{
    unsigned char buf[256];
    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"acc-test";
    opts.keepAliveInterval = 60;

    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT(len > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(buf, len), CONNECT);

    /* 完整 CONNECT 可解析，walk_len 应等于整包长度 */
    int need_len = 0, walk_len = 0;
    MQTTPacket_connectData parsed = MQTTPacket_connectData_initializer;
    ACC_ASSERT_EQ(MQTTDeserialize_connect(&parsed, buf, len, &need_len,
                                          &walk_len), 1);
    ACC_ASSERT_EQ(walk_len, len);
    ACC_ASSERT_EQ(need_len, len);
    ACC_ASSERT_EQ(parsed.keepAliveInterval, 60);
    ACC_ASSERT_EQ(parsed.clientID.lenstring.len, 8);
    ACC_ASSERT(memcmp(parsed.clientID.lenstring.data, "acc-test", 8) == 0);

    /* 半包（少 1 字节）：返回 -1，need_len 报告整包所需长度 */
    need_len = 0;
    walk_len = 0;
    ACC_ASSERT_EQ(MQTTDeserialize_connect(&parsed, buf, len - 1, &need_len,
                                          &walk_len), -1);
    ACC_ASSERT_EQ(need_len, len);
    ACC_ASSERT_EQ(walk_len, 0);

    /* CONNACK round-trip：签名是 (buf, buflen, connack_rc, sessionPresent) */
    unsigned char ackbuf[64];
    int alen = MQTTSerialize_connack(ackbuf, sizeof(ackbuf), 0, 0);
    ACC_ASSERT(alen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(ackbuf, alen), CONNACK);

    unsigned char sessionPresent = 1, connackRc = 1;
    walk_len = 0;
    ACC_ASSERT_EQ(MQTTDeserialize_connack(&sessionPresent, &connackRc,
                                          ackbuf, alen, &walk_len), 1);
    ACC_ASSERT_EQ(sessionPresent, 0);
    ACC_ASSERT_EQ(connackRc, 0);
    ACC_ASSERT_EQ(walk_len, alen);
}

ACC_TEST(publish_roundtrip_with_needlen)
{
    unsigned char buf[512];
    MQTTString topic = MQTTString_initializer;
    const char *payload = "hello";
    topic.cstring = (char *)"a/b";

    /* qos=0、非 retained 的 PUBLISH */
    int len = MQTTSerialize_publish(buf, sizeof(buf), 0, 0, 0, 0, topic,
                                    (unsigned char *)payload,
                                    (int)strlen(payload));
    ACC_ASSERT(len > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(buf, len), PUBLISH);

    /* 流式扩展：半包时返回 -1 且 need_len 报告整包所需长度 */
    unsigned char dup = 0, retained = 0;
    int qos = -1, plen = 0;
    unsigned short pid = 0;
    MQTTString rt = MQTTString_initializer;
    unsigned char *pl = NULL;
    int need_len = 0, walk_len = 0;

    ACC_ASSERT_EQ(MQTTDeserialize_publish(&dup, &qos, &retained, &pid, &rt,
                                          &pl, &plen, buf, len - 1,
                                          &need_len, &walk_len), -1);
    ACC_ASSERT_EQ(need_len, len);
    ACC_ASSERT_EQ(walk_len, 0);

    /* 补齐后整包可解析 */
    ACC_ASSERT_EQ(MQTTDeserialize_publish(&dup, &qos, &retained, &pid, &rt,
                                          &pl, &plen, buf, len,
                                          &need_len, &walk_len), 1);
    ACC_ASSERT_EQ(qos, 0);
    ACC_ASSERT_EQ(dup, 0);
    ACC_ASSERT_EQ(retained, 0);
    ACC_ASSERT_EQ(walk_len, len);
    ACC_ASSERT_EQ(need_len, len);
    ACC_ASSERT(plen == (int)strlen(payload));
    ACC_ASSERT(memcmp(pl, payload, (size_t)plen) == 0);
}

ACC_TEST_MAIN();
