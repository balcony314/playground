#include "acc_test.h"
#include "acc_string.h"

ACC_TEST(strlcpy_basic_and_truncate)
{
    char buf[8];
    ACC_ASSERT_EQ(acc_strlcpy(buf, "abc", sizeof(buf)), 3);
    ACC_ASSERT_STR_EQ(buf, "abc");
    ACC_ASSERT_EQ(acc_strlcpy(buf, "0123456789", sizeof(buf)), 10);
    ACC_ASSERT_STR_EQ(buf, "0123456"); /* 截断且 NUL 结尾 */
}

ACC_TEST(strlcat_append_and_truncate)
{
    char buf[8] = "ab";
    ACC_ASSERT_EQ(acc_strlcat(buf, "cd", sizeof(buf)), 4);
    ACC_ASSERT_STR_EQ(buf, "abcd");
    ACC_ASSERT_EQ(acc_strlcat(buf, "efghij", sizeof(buf)), 10);
    ACC_ASSERT_STR_EQ(buf, "abcdefg");
}

ACC_TEST(strcaseeq)
{
    ACC_ASSERT_EQ(acc_strcaseeq("Yes", "yES"), 1);
    ACC_ASSERT_EQ(acc_strcaseeq("a", "ab"), 0);
}

ACC_TEST(strtoll_def)
{
    ACC_ASSERT_EQ(acc_strtoll_def("42", -1), 42);
    ACC_ASSERT_EQ(acc_strtoll_def("junk", -1), -1);
    ACC_ASSERT_EQ(acc_strtoll_def("", 7), 7);
}

ACC_TEST_MAIN();
