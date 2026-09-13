# 运维手册

## 概述

本文档覆盖 gprobe 探针的部署、监控、故障排查和回滚流程。

## 部署

### 构建产物

```bash
task build      # → bin/gprobe (探针)
task examples   # → bin/target (示例目标程序)
```

### 安装方式

**方式一：直接运行二进制**

```bash
sudo ./bin/gprobe -p <PID> -f "函数名"
```

**方式二：安装到系统路径**

```bash
task install                    # 安装到 $GOPATH/bin
sudo gprobe -p <PID> -f "函数名"
```

**方式三：交叉编译后分发**

```bash
task build-linux                # → bin/gprobe-linux-amd64
scp bin/gprobe-linux-amd64 target-host:/usr/local/bin/gprobe
```

### 部署前检查

```bash
# 目标机器
uname -r                          # 内核 >= 5.4
ls /sys/kernel/btf/vmlinux        # BTF 必须存在
file /proc/<PID>/exe              # 目标进程存在
nm <binary> | grep <函数名>        # 目标函数存在且有符号
```

## 命令行参考

```
gprobe [flags]

Flags:
  -p, --pid int         目标进程 PID
  -b, --binary string   目标二进制文件路径
  -f, --func strings    要 hook 的函数名 (可多次指定)
  -o, --output string   输出格式: terminal (默认) | json
  -h, --help            帮助信息
```

### 常用场景

```bash
# 追踪单个函数
sudo gprobe -p 12345 -f "main.HandleRequest"

# 追踪多个函数
sudo gprobe -p 12345 -f "main.Add" -f "main.Multiply" -f "main.Divide"

# JSON 输出 (适合日志采集)
sudo gprobe -p 12345 -f "main.Add" -o json >> /var/log/gprobe.log

# 通过二进制路径指定 (无需 PID)
sudo gprobe -b /usr/local/bin/myapp -f "main.Add"
```

## 监控

### 运行状态检查

```bash
# 探针进程存活
ps aux | grep gprobe

# 已挂载的 uprobe
sudo cat /sys/kernel/debug/tracing/uprobe_events

# BPF 程序运行统计
sudo bpftool prog list | grep gprobe

# 事件丢失告警 (stderr 输出)
# gprobe: 警告 - 丢失 <N> 个事件
```

### 告警指标

| 指标 | 检查方式 | 阈值 |
|------|---------|------|
| 探针存活 | `ps aux \| grep gprobe` | 必须存在 |
| 事件丢失 | stderr 中的丢失计数 | > 100/min 告警 |
| BPF 程序状态 | `bpftool prog list` | 状态应为 loaded |
| 目标进程存活 | `ps -p <PID>` | 必须存在 |

### 性能影响

- 单次 hook 延迟：**1-3μs**（uprobe 标准开销）
- CPU 开销：与被 hook 函数的调用频率成正比
- 内存占用：固定（per-cpu 环形缓冲区）
- 建议：高频函数 (> 10K calls/s) 谨慎 hook

## 常见问题

### 1. 权限不足

```
错误: loading eBPF programs: permission denied
```

**原因：** eBPF 需要 `CAP_BPF` 或 root 权限。

**解决：**

```bash
sudo ./bin/gprobe ...
```

### 2. 目标进程不存在

```
错误: 进程 <PID> 不存在或无法访问
```

**解决：**

```bash
ps aux | grep <进程名>       # 确认 PID
sudo gprobe -p <正确PID> ...
```

### 3. uprobe 挂载失败

```
错误: attaching uprobe: no such process / symbol not found
```

**原因：** 目标函数符号不存在或二进制缺少调试信息。

**解决：**

```bash
# 检查函数符号
nm <binary> | grep <函数名>

# 确保目标程序编译时包含调试信息
go build -gcflags="all=-N -l" -o myapp .
```

### 4. 事件丢失

```
gprobe: 警告 - 丢失 <N> 个事件
```

**原因：** 用户态消费速度低于内核态产生速度。

**解决：**

- 减少 hook 的函数数量
- 使用 JSON 输出替代终端输出（终端格式化更慢）
- 将输出重定向到文件而非终端

### 5. BTF 支持缺失

```
错误: BTF is required but not available
```

**解决：**

```bash
ls /sys/kernel/btf/vmlinux    # 检查是否存在
# 如果不存在，升级内核到 5.4+ 或安装 linux-headers
```

### 6. 目标程序需要调试信息

**现象：** 可以挂载但参数解析异常或程序崩溃。

**原因：** Go 程序默认编译会优化/内联部分代码。

**解决：** 目标程序编译时添加 `-gcflags="all=-N -l"` 禁用优化和内联：

```bash
go build -gcflags="all=-N -l" -o myapp .
```

## 回滚

### 停止探针

探针本身是无侵入的，停止后目标进程不受影响：

```bash
# 正常停止 (Ctrl+C 或 SIGTERM)
kill -TERM <gprobe_PID>

# 强制停止
kill -9 <gprobe_PID>
```

停止后 uprobe 自动卸载，目标进程恢复原始指令。

### 验证回滚

```bash
# 确认 uprobe 已卸载 (应无输出)
sudo cat /sys/kernel/debug/tracing/uprobe_events | grep <函数名>

# 确认 BPF 程序已卸载
sudo bpftool prog list | grep gprobe
```

### 清理残留

极少数情况（如探针崩溃）可能留下未清理的 uprobe：

```bash
# 手动清理 uprobe
echo > /sys/kernel/debug/tracing/uprobe_events

# 或逐个删除
echo "-:uprobe_events/<函数名>" >> /sys/kernel/debug/tracing/uprobe_events
```

## 安全注意事项

- 探针需要 **root 权限**，部署时限制访问
- uprobe 可读取目标进程的寄存器状态和内存，确保目标环境受信
- JSON 输出可能包含敏感数据（函数参数值），注意日志存储安全
- 生产环境建议配合 systemd 管理探针生命周期

## systemd 集成示例

```ini
# /etc/systemd/system/gprobe.service
[Unit]
Description=gprobe eBPF tracer
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/gprobe -p 12345 -f "main.HandleRequest" -o json
ExecStop=/bin/kill -TERM $MAINPID
Restart=on-failure
RestartSec=5
StandardOutput=append:/var/log/gprobe.log
StandardError=append:/var/log/gprobe-error.log

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gprobe
sudo systemctl status gprobe
journalctl -u gprobe -f
```
