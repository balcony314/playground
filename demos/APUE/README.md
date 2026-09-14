# APUE 学习示例

《UNIX 环境高级编程》(Advanced Programming in the UNIX Environment, 第 3 版)书中示例代码练习,按章节组织,文件名与书中图号对应(如 `3.1.c` 对应图 3-1)。

## 目录结构

```text
APUE/
├── install.sh      # 编译安装 apue 源码包(头文件 + 静态库)
├── apue.3e.tar.gz  # apue.3e 官方源码包
├── 3/              # 第 3 章:文件 I/O(open / read / write / lseek)
├── 4/              # 第 4 章:文件和目录(stat / 目录遍历 / 文件权限)
├── 5/              # 第 5 章:标准 I/O 库(流操作 / 缓冲)
└── 7/              # 第 7 章:进程环境(main / 环境变量 / atexit)
```

## 环境准备

示例依赖 `apue.h` 和 `libapue.a`,先运行安装脚本:

```bash
sudo ./install.sh
```

脚本会解压并编译 `apue.3e` 源码包,将以下文件安装到系统目录后清理临时文件:

| 文件 | 安装位置 |
|------|----------|
| `apue.h` | `/usr/local/include/` |
| `error.c` | `/usr/include/` |
| `libapue.a` | `/usr/local/lib/` |

> 如编译失败,先安装依赖:`sudo apt-get install libbsd-dev`

## 编译运行

单个示例:

```bash
gcc 3/3.1.c -lapue -o test
./test
```

某章全部示例:

```bash
cd 3
gcc *.c -lapue   # 需逐个编译时,请单独指定源文件
```

## 进度

- [x] 第 3 章 文件 I/O
- [x] 第 4 章 文件和目录
- [x] 第 5 章 标准 I/O 库
- [x] 第 7 章 进程环境
