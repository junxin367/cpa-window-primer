# CPA Window Primer

CLIProxyAPI 插件，用于在配置的每日时间窗口前，对选中的 OpenAI OAuth 认证文件发送一次轻量 `hi` 请求，提前触发约 5 小时可用窗口。

插件名称保持 provider-neutral，因为它处理的是认证文件窗口预热，不绑定某一个客户端品牌。当前筛选范围是 CPA 暴露的 Codex/OpenAI OAuth auth 记录。

## 行为

- 默认模型：`gpt-5.4`
- 默认内容：`hi`
- 默认目标窗口：`07:00`、`12:00`、`17:00`
- 触发窗口：每个目标时间前 1 分钟，例如 `06:59:00-07:00:00`
- 最小间隔：同一认证文件按最近一次成功预热时间计算，默认 `5h`
- 调度固定：内部请求携带 `X-CPA-Window-Primer-Auth-ID`，插件 scheduler 会选择匹配的认证文件

如果到达目标窗口时，同一认证文件尚未满足 `last_success_at + 5h`，插件会在这一分钟内等待到可发送时间；如果等不到，则跳过该窗口并记录 `min_interval_not_met`，避免还没满 5 小时就触发。

## 管理页

安装并启用插件后，在 CPA 左侧菜单打开 **窗口预热**：

```text
/v0/resource/plugins/cpa-window-primer/status
```

管理页提供：

- CPA 管理密钥输入，用于保存配置、刷新状态、手动预热。
- OpenAI OAuth 认证文件多选。
- 发送时间窗口多选，默认 `07:00`、`12:00`、`17:00`。
- 模型、prompt、最小间隔、提前触发窗口、后台检查间隔配置。
- 最近成功时间、最近尝试结果、下次可发送时间展示。
- 对单个认证文件手动执行一次预热。

插件不再依赖 CPA 自动生成的 ConfigFields 表单；配置入口以该中文管理页为准。

## 构建

```powershell
.\scripts\build.ps1
```

构建产物输出到 `dist/`。

手动构建：

```bash
go test ./...
go build -buildmode=c-shared -o dist/cpa-window-primer.dll .
```

Linux 使用 `.so`，macOS 使用 `.dylib`。

## CPA 配置

插件商店源 URL：

```text
https://raw.githubusercontent.com/junxin367/cpa-window-primer/main/registry.json
```

CPA 插件商店需要填写 registry manifest JSON URL。不要把 GitHub 仓库主页 URL 当作商店源。

手动安装时，动态库文件名需要和插件 ID 保持一致：

```text
plugins/cpa-window-primer.dll
```

最小 `config.yaml` 示例：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cpa-window-primer:
      enabled: true
      priority: 100
```

插件运行配置会由管理页保存到用户配置目录下的插件配置文件。

## Management API

```text
GET  /v0/management/cpa-window-primer/snapshot
GET  /v0/management/cpa-window-primer/config
PUT  /v0/management/cpa-window-primer/config
GET  /v0/management/cpa-window-primer/state
POST /v0/management/cpa-window-primer/run
```

手动预热 payload：

```json
{
  "auth_id": "selected-auth-id",
  "force": false
}
```

`force=false` 会遵守 5 小时最小间隔。`force=true` 仅建议调试时使用。
