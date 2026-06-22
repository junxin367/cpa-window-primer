# CPA Window Primer 插件设计

## 目标

为 CLIProxyAPI 开发一个独立插件，用于对选中的 OAuth 认证文件在指定时间窗口前发送一次轻量 `hi` 请求，提前触发约 5 小时可用窗口。当前目标范围是 Codex/OpenAI/Claude/Anthropic auth。

默认行为：

- 认证范围：Codex/OpenAI/Claude/Anthropic auth。
- 默认模型：`gpt-5.4`。
- 默认提示词：`hi`。
- 默认时间：`07:00`、`12:00`、`17:00`。
- 触发窗口：在目标时间前 1 分钟内触发，即 `[target-1m, target)`。
- 时区：使用 CPA 运行机器的本地时区。

## CPA 插件能力组合

插件使用三类 CPA 插件能力：

- `Management API`：展示 auth 列表、保存多选 auth 与多选时间、展示最近执行结果、提供手动触发入口。
- `host.auth.*` 回调：读取宿主 auth 文件列表与运行态信息，只筛选 Codex/OpenAI/Claude/Anthropic auth。
- `host.model.execute` + `scheduler.pick`：通过宿主模型执行路径发送 warmup 请求，并在调度阶段强制选中目标 auth。

`host.model.execute` 当前没有直接暴露 `pinned_auth_id` metadata，因此插件通过内部 header 传递目标 auth：

- warmup 请求设置 `X-CPA-Window-Primer-Auth-ID: <auth_id>`。
- 插件自己的 `scheduler.pick` 读取 `SchedulerOptions.Headers`。
- 如果候选列表包含该 auth，则返回 `SchedulerPickResponse{Handled:true, AuthID:<auth_id>}`。
- 若 header 缺失、auth 不在候选列表中，插件不处理本次调度，交回 CPA 默认调度逻辑。

## 配置与状态

用户配置支持：

- `auth_ids`: 多选 auth ID。
- `times`: 多选每日目标时间，格式 `HH:mm`。
- `model`: 默认 `gpt-5.4`。
- `prompt`: 默认 `hi`。
- `enabled`: 是否启用后台 warmup。
- `min_interval`: 默认 `5h`，用于防止同一 auth 在 5 小时内重复成功触发。

插件运行状态持久化到用户配置目录下的插件状态文件，例如：

```text
<UserConfigDir>/CLIProxyAPI/cpa-window-primer/state.json
```

状态内容包括：

- 每个 auth 的最近成功 warmup 时间。
- 每个 auth 最近一次尝试时间、结果、HTTP 状态、错误摘要。
- 每个 auth 每个日期窗口的执行标记，避免同一窗口重复发送。

## 5 小时间隔规则

调度不能只依赖固定时间点，因为在 `[target-1m, target)` 内不同秒数触发时，连续窗口可能出现实际间隔不足 5 小时。

插件按以下规则处理：

1. 对每个 auth 独立记录 `last_success_at`。
2. 每次进入目标时间前 1 分钟窗口时，计算 `earliest_allowed = last_success_at + 5h`。
3. 只有 `now >= earliest_allowed` 时才允许发送。
4. 如果 `earliest_allowed < target`，插件在窗口内等待到 `earliest_allowed` 后发送。
5. 如果 `earliest_allowed >= target`，本次窗口跳过，并记录原因 `min_interval_not_met`。
6. 如果某次 warmup 失败，不更新 `last_success_at`；失败只记录为最近尝试结果。

默认时间 `07:00`、`12:00`、`17:00` 的计划触发点分别是 `06:59`、`11:59`、`16:59`。在正常情况下相邻计划点正好间隔 5 小时；如果实际触发有秒级延迟，下一窗口会等待到满 5 小时后再发，仍保持在目标时间前 1 分钟窗口内。

对于用户自定义时间，如果两个窗口间隔不足 5 小时，后一个窗口会被自动跳过，而不是强行发送。

## 后台调度流程

插件启动或配置变更后启动后台 goroutine：

1. 每 5 秒读取当前本地时间。
2. 对配置中的每个时间生成当天窗口 `[target-1m, target)`。
3. 判断当前时间是否处于某个窗口内。
4. 对每个选中的 auth 执行去重与 5 小时间隔校验。
5. 构造 OpenAI Chat Completions 非流式请求：

```json
{
  "model": "gpt-5.4",
  "stream": false,
  "messages": [
    {
      "role": "user",
      "content": "hi"
    }
  ]
}
```

6. 调用 `host.model.execute`，`entry_protocol=openai`，`exit_protocol=openai`。
7. 记录结果并写入状态文件。

## Management 页面与 API

插件提供只读资源页：

```text
/v0/resource/plugins/cpa-window-primer/status
```

资源页展示：

- 可选 Codex/OpenAI/Claude/Anthropic auth。
- 当前选中 auth。
- 当前选中时间。
- 最近执行结果。
- 下次候选窗口。
- 被跳过窗口及原因。

写操作通过认证的 Management API 完成：

```text
GET  /v0/management/cpa-window-primer/config
PUT  /v0/management/cpa-window-primer/config
POST /v0/management/cpa-window-primer/run
```

`run` 用于手动触发某个 auth 的 warmup，默认仍遵守 5 小时间隔；可设计 `force=true` 仅用于调试，但默认不暴露在 UI 主路径中。

## 错误处理

- 未选择 auth：后台调度不执行，状态页提示需要配置。
- auth 不可用或不在候选列表：记录 `auth_not_candidate`。
- 模型不可路由：记录 CPA 返回的错误摘要。
- 请求失败：记录 HTTP 状态和响应摘要，不更新 `last_success_at`。
- 状态文件写入失败：继续内存态运行，并在状态页显示持久化错误。

## 测试策略

实现时保留长期有价值的单元测试：

- 时间窗口解析与排序。
- `[target-1m, target)` 窗口判定。
- 5 小时间隔校验，覆盖秒级延迟场景。
- 自定义窗口间隔不足 5 小时时跳过。
- scheduler header 选择指定 auth。
- auth 过滤仅保留 Codex/OpenAI/Claude/Anthropic。

不为单次排查创建临时测试文件；如果临时测试用于验证，任务结束前删除。

## 发布与部署

仓库名：

```text
cpa-window-primer
```

插件使用 Go 实现并构建为 CPA C ABI 动态库：

- Windows: `.dll`
- Linux: `.so`
- macOS: `.dylib`

CPA 配置示例：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cpa-window-primer:
      enabled: true
      priority: 100
```

插件的 scheduler 只处理带 `X-CPA-Window-Primer-Auth-ID` 的内部 warmup 请求，不影响普通用户请求调度。
