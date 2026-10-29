# CPA Window Primer

CLIProxyAPI 插件，支持读取 CPA 宿主中的所有凭证（如 Codex、Devin、Kimi、Claude、OpenAI、Gemini 等），在凭证列表展示对应类型，并在配置的每日时间窗口前发送轻量 `hi` 请求，同时支持独立配置额度汇总推送。具体额度窗口由提供商决定。

插件名称保持 provider-neutral，因为它处理的是认证文件窗口预热，不绑定某一个客户端品牌。当前筛选范围是 CPA 暴露的 Codex/OpenAI/Claude/Anthropic/Devin auth 记录；Devin 需要先在支持该提供商的 CPA 中完成登录。

## 行为

- 默认模型：`gpt-6-luna, claude-sonnet-4-6`
- 默认内容：`hi`
- 默认目标窗口：`07:00`、`12:00`、`17:00`
- 默认额度推送时间：`10:00`、`15:00`
- 额度推送：仅工作日按配置时间定时推送；手动立即推送不受工作日限制
- 预热与额度推送分别选择凭证、设置时间和开关；关闭预热不会停止额度推送。旧配置首次加载时，将原凭证选择复制到额度推送；之后分别保存，清空一组不会影响另一组。
- GPT 额度按接口窗口时长归类：5 小时额度和周额度分别加权汇总；只有周额度的账号只参与周统计。如果某组账号都没有 5 小时额度，该组的预览和推送不显示 5 小时额度。
- Devin 额度通过宿主读取 session token 并查询额度，单独显示为 DEVIN，不计入 GPT/Claude 的 5 小时额度。Max 仅显示周额度，忽略接口携带的日额度占位字段；Pro / Teams 等账号按实际返回的日、周窗口展示。Devin 账号按等权汇总；缺失或解析失败的额度不作为可用额度统计。
- 触发窗口：每个目标时间前 1 分钟，例如 `06:59:00-07:00:00`
- 最小间隔：同一认证文件按最近一次成功预热时间计算，默认 `5h`
- 无额度跳过：额度接口确认用尽，或预热返回额度不足时，插件暂停该认证文件的定时预热，并显示预计恢复时间
- 额度恢复：后台每分钟复查已选择且受阻的认证文件；查询确认恢复后立即清除本地阻塞。状态刷新和额度预览也会同步结果，查询失败或缺少额度数据时保留原阻塞
- 页面同步：填写管理密钥后，可见页面每 30 秒刷新状态，不覆盖尚未保存的配置；最近预热保留历史结果，悬停可查看完整错误
- 模型兜底：配置模型不可用时尝试同提供商的可用文本模型；读取宿主模型列表失败时使用项目默认模型，仍固定使用目标认证文件
- 调度固定：内部请求携带 `X-CPA-Window-Primer-Auth-ID`，插件 scheduler 会选择匹配的认证文件

如果到达目标窗口时，同一认证文件尚未满足 `last_success_at + 5h`，插件会在这一分钟内等待到可发送时间；如果等不到，则跳过该窗口并记录 `min_interval_not_met`，避免还没满 5 小时就触发。

## 管理页

安装并启用插件后，在 CPA 左侧菜单打开 **窗口预热**：

```text
/v0/resource/plugins/cpa-window-primer/status
```

管理页提供：

- CPA 管理密钥输入，用于保存配置、刷新状态、手动预热；密钥会保存在浏览器本地，已保存的后台预热不依赖本页面持续打开。
- 在凭证区通过“凭证配置 / 预热设置 / 额度推送设置”导航切换，两边的选择、全选、清空和保存互不影响，切换保留未保存的输入。
- 凭证列表支持读取所有 CPA 凭证（包含 Codex、Devin、Kimi 等），并单独展示“类型”列；手动禁用的账号不可选，无额度或冷却中的账号仍可保存选择，但后台定时预热会自动跳过。
- 额度预览和立即推送使用已保存的额度推送凭证；修改后需先点击“保存额度推送设置”。插件只保存凭证 ID，Devin session token 不写入插件配置或页面。
- 额度预览使用与推送相同的消息模板，保留分组标题、等宽进度条、剩余百分比、颜色和重置时间。
- 发送时间窗口多选，默认 `07:00`、`12:00`、`17:00`。
- 模型按提供商智能识别：若凭证属于不同渠道（如 Kimi、Claude、GPT、Gemini 等），插件将自动识别对应渠道的可用模型进行预热，配置中显式填写的匹配模型优先。\n- 预热内容、最小间隔、提前触发窗口、后台检查间隔配置。
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
GET  /v0/management/cpa-window-primer/usage
POST /v0/management/cpa-window-primer/usage-push
```

配置 API 支持部分更新。`auth_ids` 是预热凭证，`usage_auth_ids` 是额度推送凭证；额度推送设置使用 `usage_auth_ids`、`usage_push_enabled`、`usage_push_times`、`webhook_url`，预热继续使用 `enabled`、`times`、`model` 等原字段。省略字段保留原值，`usage_auth_ids: []` 明确清空额度凭证。

手动预热 payload：

```json
{
  "auth_id": "selected-auth-id",
  "force": false
}
```

`force=false` 会遵守 5 小时最小间隔。管理页的单个/批量手动预热会使用 `force=true`，用于确认指定认证文件能否立刻打通；手动预热成功不会更新后台定时使用的 `last_success_at`，因此不会打乱 07:00 / 12:00 / 17:00 等定时窗口。

Codex 预热默认通过宿主模型执行路径，并在调度阶段指定目标认证文件。如果 CLIProxyAPI 因优先级或当前模型状态未把目标认证放入候选列表，插件会通过 `host.auth.get` 读取该认证文件自身的 OAuth 凭据，再定向请求固定的 ChatGPT Codex Responses API。未配置认证专属代理时，请求使用宿主流式 HTTP 回调以继承宿主全局代理；配置了 `proxy_url` 或 `direct` 时，兜底请求会严格使用该认证自己的网络出口策略。兜底请求最多等待两分钟，并在收到 `response.completed` 后立即关闭流。只有目标请求返回该完成事件才会记为成功，不会回退到其他认证文件冒充成功。

受当前宿主回调接口限制，定向兜底依赖 CLIProxyAPI 已持久化的最新 OAuth Token；结果会按目标 `auth_id` 写入插件状态，但不会增加宿主管理页中的该认证 `Success` / `Failed` 计数。
