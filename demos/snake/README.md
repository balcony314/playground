# 贪吃蛇(Electron + Vue 3 + TypeScript)

基于 Electron、Vue 3 与 TypeScript 的桌面贪吃蛇小游戏,逻辑与渲染分离,核心规则附单元测试。

## 常用命令

通过 [Taskfile](https://taskfile.dev) 管理(需安装 `task` 与 `pnpm`),`task` 或 `task --list` 查看全部任务:

```bash
task install     # 安装依赖
task dev         # 启动开发模式(electron-vite 热更新)
task test        # 运行 Vitest 单元测试
task typecheck   # 运行主进程 + 渲染进程 TypeScript 类型检查
task build       # 构建生产产物(out/)
task clean       # 清理构建产物(out/、dist/)
```

等价的 pnpm 脚本(`pnpm install/dev/test/typecheck/build`)仍然可用。

## 打包分发

打包配置见 `electron-builder.yml`,产物输出到 `dist/`:

```bash
task package:linux  # Linux:AppImage + deb
task package:win    # Windows:NSIS 安装包 + 便携版 exe
task package:all    # 当前平台所有目标
```

- 推荐在对应平台上原生打包;在 Linux 上交叉打包 Windows 包需安装 `wine`
- 图标暂用 Electron 默认,如需自定义在 `build/icon.png` 补充后于配置中引用

## 操作方式

- 移动:方向键或 WASD
- 空格:开始 / 暂停 / 继续
- 回车:游戏结束后再来一局
