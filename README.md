# Telegram C2

基于 Telegram Bot API 的轻量级 C2 工具。在 Telegram 聊天窗口中直接发送命令, Agent 在目标机器上执行并回复结果。支持手机操作, 无需 VPS 或公网 IP。

## 项目结构

```
telegram_c2/
├── build_agent.go      # 构建工具 (含 Agent 模板源码)
├── go.mod / go.sum     # Go 模块依赖
├── README.md           # 本文件
├── ROADMAP.md          # 功能完善计划
└── dist/               # 编译产物 (构建后生成)
```

## 架构

```
用户 (手机/PC Telegram)
  |  输入: whoami
  v
Telegram Bot API
  |
  v
Agent (目标机器)
  |  执行命令
  v
Telegram Bot API -- 结果直接回复到聊天窗口
  |
  v
用户 (手机/PC Telegram)
```

## 功能

- 命令执行 (cmd.exe / sh)
- 文件下载 (sendDocument API) 和上传 (URL 下载)
- 截图 (sendPhoto API, 不写磁盘)
- SOCKS5 代理 (密码认证, 防火墙管理)
- 多 Agent 管理 ([A] [B] 前缀路由)
- 主动退出 (自删除 + 痕迹清理)
- 跨平台编译 (Windows / Linux / macOS)
- 代理支持 (HTTP/HTTPS)
- 无窗口后台运行 (Windows)

## 快速开始

### 1. 创建 Telegram Bot

在 Telegram 搜索 @BotFather, 发送 `/newbot`, 设置名称和用户名, 得到 BOT_TOKEN。

给 Bot 发一条任意消息, 然后执行:

```bash
curl -s "https://api.telegram.org/bot<BOT_TOKEN>/getUpdates"
```

找到 `"chat":{"id": 123456789}`, 这就是 CHAT_ID。

### 2. 编译构建工具

```bash
cd telegram_c2
go build -ldflags="-s -w" -o build_agent.exe build_agent.go
```

### 3. 生成 Agent

```bash
# 双击 build_agent.exe, 或命令行:
build_agent.exe
```

按提示输入:
- Bot Token
- Chat ID
- Proxy (可选,主要是agent连接Telegram的网络问题 如 http://127.0.0.1:7890, 回车跳过)
- Agent ID (A/B/C..., 多 Agent 时区分)
- exe 文件名
- 目标平台 (windows/linux/macos)

产物: `dist/tg_agent.exe` (Windows) 或 `dist/tg_agent_linux` (Linux)
<img width="702" height="121" alt="image" src="https://github.com/user-attachments/assets/a319a081-29ee-4f77-81a9-49caab57f9c4" />

### 4. 上线

双击生成的 exe (无窗口后台运行), 在 Telegram 聊天框则能收到agent回传的消息
<img width="805" height="130" alt="image" src="https://github.com/user-attachments/assets/fd1112a9-d515-4e60-8d9c-b53833acfc65" />

### 5. 多 Agent 管理

```
[ON] Agent online | [A] LAPTOP-A | windows
[ON] Agent online | [B] SERVER-B | linux

[A] whoami      # 仅 Agent A 执行
[B] dir /home   # 仅 Agent B 执行
whoami          # 所有 Agent 执行
sessions        # 列出所有 Agent
```
<img width="805" height="1014" alt="image" src="https://github.com/user-attachments/assets/971992df-8570-4eab-bd93-0715a7791cdf" />


```
帮助菜单可自己再机器人处添加
info - 系统信息
screenshot - 截屏
download - 下载文件 download D:\a.txt
upload - 上传URL到目标 upload http://...
socks - SOCKS代理 on/off/status SOCKS代理: on 端口 --pass 密码
sessions - 列出所有Agent
exit - 退出Agent
```
<img width="806" height="250" alt="image" src="https://github.com/user-attachments/assets/eeb12e46-0f8a-412c-8ba4-8cd1a33ee811" />

## 命令列表

| 命令 | 说明 |
|------|------|
| `info` | 系统信息 |
| `pwd` | 当前目录 |
| `cd <dir>` | 切换目录 |
| `screenshot` | 截屏 |
| `download <path>` | 下载文件到 Telegram |
| `upload <url>` | 从 URL 下载到目标 |
| `socks on <port> --pass <pw>` | 启动 SOCKS5 代理 |
| `socks off` | 关闭 SOCKS 代理 |
| `sessions` | 列出所有 Agent |
| `exit` | 退出并自删除 |
| 任意 shell 命令 | 通过 cmd/sh 执行 |

命令前可加 `[A]` 前缀指定目标 Agent。也支持 `/` 前缀 (如 `/info`), 兼容 BotFather 菜单。

## 跨平台编译

```bash
build_agent.exe
# 选择目标平台: windows / linux / macos
# 自动编译对应平台二进制
```

## 注意事项

- Agent exe 中内嵌了 Bot Token, 分发时等同于泄露凭证, 请妥善保管
- 如果 Token 泄露, 立即去 @BotFather 执行 /revoke 撤销
- Telegram API 在中国大陆可能需要代理访问

## License

仅供授权的安全研究和渗透测试使用。
