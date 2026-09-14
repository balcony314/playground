cmake_minimum_required(VERSION 3.16)
project(acc C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE Release)
endif()

add_compile_options(-Wall -Wextra -Werror -D_GNU_SOURCE)

find_package(Threads REQUIRED)
find_package(OpenSSL REQUIRED)

file(GLOB_RECURSE ACC_SOURCES CONFIGURE_DEPENDS ${CMAKE_CURRENT_SOURCE_DIR}/src/*.c)
add_executable(acc ${ACC_SOURCES})
target_include_directories(acc PRIVATE src)
target_link_libraries(acc PRIVATE Threads::Threads OpenSSL::SSL OpenSSL::Crypto mqttpacket)

# vendored MQTTPacket（Eclipse Paho，含 need_len/walk_len 流式解析扩展）
file(GLOB MQTTPACKET_SOURCES third_party/mqttpacket/*.c)
add_library(mqttpacket STATIC ${MQTTPACKET_SOURCES})
target_include_directories(mqttpacket PUBLIC third_party/mqttpacket)
target_compile_options(mqttpacket PRIVATE -Wno-sign-compare -Wno-unused-parameter -Wno-error)
# -Wno-error 中和目录级 -Werror，vendored 代码告警不致命

enable_testing()

file(GLOB ACC_TEST_SOURCES tests/test_*.c)
foreach(t ${ACC_TEST_SOURCES})
    get_filename_component(name ${t} NAME_WE)
    add_executable(${name} ${t})
    target_include_directories(${name} PRIVATE src src/util src/event src/net src/mqtt src/core tests)
    file(GLOB ACC_UTIL_SRC src/util/*.c)
    file(GLOB ACC_EVENT_SRC src/event/*.c)
    file(GLOB ACC_NET_SRC src/net/*.c)
    file(GLOB ACC_MQTT_SRC src/mqtt/*.c)
    # core 参与：acc_channel.c 的 CLOSE/OPEN_CHANNEL 分支引用进程表
    file(GLOB ACC_CORE_SRC src/core/*.c)
    target_sources(${name} PRIVATE ${ACC_UTIL_SRC} ${ACC_EVENT_SRC} ${ACC_NET_SRC} ${ACC_MQTT_SRC} ${ACC_CORE_SRC})
    target_link_libraries(${name} PRIVATE Threads::Threads OpenSSL::SSL OpenSSL::Crypto mqttpacket)
    add_test(NAME ${name} COMMAND ${name})
endforeach()

# e2e 客户端：spawn acc 实例跑协议用例（独立 target，不在 tests/test_*.c
# glob 内；输出到 build/tests/e2e/，由 task e2e 驱动）
add_executable(acc_e2e tests/e2e/acc_e2e.c)
target_include_directories(acc_e2e PRIVATE src tests third_party/mqttpacket)
target_link_libraries(acc_e2e PRIVATE Threads::Threads OpenSSL::SSL OpenSSL::Crypto mqttpacket)
set_target_properties(acc_e2e PROPERTIES
    RUNTIME_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/tests/e2e)
