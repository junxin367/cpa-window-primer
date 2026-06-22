# CPA Window Primer

CLIProxyAPI plugin for priming selected OAuth auth session windows by sending a small `hi` request before configured daily windows.

The initial supported auth scope is Codex/OpenAI OAuth auth records exposed by CPA. The plugin name stays provider-neutral because the behavior is about auth/session windows rather than one provider brand.

## Behavior

- Default model: `gpt-5.4`
- Default prompt: `hi`
- Default target windows: `07:00`, `12:00`, `17:00`
- Trigger window: one minute before each target, for example `06:59:00-07:00:00`
- Minimum interval: `5h` per auth, based on the last successful primer request
- Scheduler pinning: internal requests carry `X-CPA-Window-Primer-Auth-ID`, and the plugin scheduler selects the matching auth candidate

If a target window is reached but the selected auth has not reached `last_success_at + 5h`, the plugin either waits until the interval is satisfied inside that one-minute window or skips the window with `min_interval_not_met`.

## Build

```powershell
.\scripts\build.ps1
```

The build script writes artifacts to `dist/`.

Manual build:

```bash
go test ./...
go build -buildmode=c-shared -o dist/cpa-window-primer.dll .
```

Use `.so` on Linux and `.dylib` on macOS.

## CPA Configuration

Build the dynamic library and place it in CPA's plugin directory using a basename that matches the plugin ID:

```text
plugins/cpa-window-primer.dll
```

Example `config.yaml`:

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cpa-window-primer:
      enabled: true
      priority: 100
      auth_ids: []
      times:
        - "07:00"
        - "12:00"
        - "17:00"
      model: "gpt-5.4"
      prompt: "hi"
      min_interval: "5h"
```

## Management API

Resource page:

```text
/v0/resource/plugins/cpa-window-primer/status
```

Authenticated management routes:

```text
GET  /v0/management/cpa-window-primer/config
PUT  /v0/management/cpa-window-primer/config
GET  /v0/management/cpa-window-primer/state
POST /v0/management/cpa-window-primer/run
```

Manual run payload:

```json
{
  "auth_id": "selected-auth-id",
  "force": false
}
```

`force=false` respects the five-hour minimum interval. `force=true` is intended for debugging only.
