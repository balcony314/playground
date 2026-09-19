import { contextBridge } from 'electron'

// 本游戏不需要主进程 API,仅保留最小桥接占位
contextBridge.exposeInMainWorld('electron', { platform: process.platform })
