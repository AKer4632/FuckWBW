# 万步健康 · 自动刷步

账号密码登录 → 自动拉绑定设备 → 上报步数（达标朝暮任务）。

## 项目目标

既然这是"任务", 那为何要卖"摇步器", 免费下发的设备还不支持手机上传必须用电脑操作, 挑的Micro USB, 一根线卖15🤣去抢银行算了

想要干什么, 一目了然

这不制裁?

## 鸣谢

### AI模型

Gemini 3.5 Flash & Grok 4.5

### 工具

- Fiddler Everywhere
- Reqable
- OpenCode Desktop

### 特别鸣谢

- [北京万步健康科技有限公司](https://www.wanbu.com.cn/NewWanbu/App/NewHome/) 的**程序员🥰🥰🥰

## 本地 Web

```bash
go run .
# 打开 http://localhost:8080/
```

## 本地 CLI（多账号）

```bash
# Windows PowerShell
$env:ACCOUNTS = @"
12222222222:密码1
13800138000:密码2
"@
$env:STEPS = "random"   # 或 13000 固定
go run ./cmd/auto/
```

## GitHub Actions（每天 19:00 北京时间）

### 1. 推送到 GitHub

把本仓库推到你的 GitHub 仓库。

### 2. 配置 Secrets

仓库 → **Settings** → **Secrets and variables** → **Actions** → **New repository secret**

| Name | 必填 | 说明 |
|------|------|------|
| `ACCOUNTS` | 是 | 多账号，见下方格式 |
| `STEPS` | 否 | `random`（默认）= 每账号 10000~13000 随机；或填固定数字 |

**ACCOUNTS 格式（推荐多行）：**

```text
12222222222:密码1
13800138000:密码2
```

**或 JSON：**

```json
[
  {"account":"12222222222","password":"密码1"},
  {"account":"13800138000","password":"密码2"}
]
```

### 3. 触发方式

- **定时**：每天 UTC 11:00（北京时间 19:00）自动跑  
- **手动**：Actions → `daily-steps` → Run workflow（可临时改步数）

### 4. 查看结果

Actions 日志里会打出每个账号成功/失败（账号中间打码）。

## 说明

- 账号必须已绑定 TW 系列计步器（`getUserDeviceNew` 能返回 serial）
- 单账号超时约 5 分钟，多账号之间间隔 5 秒
- 工作流文件：`.github/workflows/daily-steps.yml`
