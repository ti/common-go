# Logging

`github.com/ti/common-go/log` writes one JSON object per line to stdout (Go's `log/slog` JSON handler), so log pipelines (Loki, ELK, CloudWatch) can index fields without parsing.

```json
{"time":"2026-11-05T10:00:00.123+08:00","level":"INFO","msg":"POST","action":"/example.user.v1.UserService/CreateUser","request_id":"3f2b...","ip":"10.0.0.7","protocol":"HTTP/2.0","status":200,"user_id":"u-1"}
```

| Field | Meaning |
|---|---|
| `time`, `level`, `msg` | Standard slog fields (`DEBUG`, `INFO`, `WARN`, `ERROR`) |
| `action` | What happened: for requests the RPC `/pkg.Service/Method` (HTTP paths are cut to their first two segments to keep cardinality low); for your own logs, the name you pass to `Action` |
| `request_id` | `x-request-id` from the request, or generated; propagated as gRPC metadata |
| `ip`, `peer`, `protocol`, `status`, `method` | Added to access logs |
| `project_id`, `client_id`, `user_id`, `device_id`, `organization_id` | From the `mux.AuthInfo` your auth function stored |
| `request`, `response` | Bodies, when `logBody` is enabled |

## API

```go
log.Action("SyncUsers").Info("synced") // message only
log.Action("SyncUsers").Info("synced %d users", n) // printf-style when arguments are given
log.With(map[string]any{"action": "SyncUsers", "count": n}).Warn("slow sync")

// Request-scoped logger: carries request_id, user_id, ... of the current request.
log.Extract(ctx).Action("CreateUser").Info("created")
log.Inject(ctx, map[string]any{"user_id": id}) // add fields to the request's access log line

ctx = log.NewContext(ctx, map[string]any{"job": "nightly"}) // for work outside requests
log.SetLevel("info") // debug (default), info, warn, error; set from config Log.Level
```

The message argument is a format string only when extra arguments are passed; key/value pairs are not supported, use `With` for fields. `Fatal` logs the message through the standard `log` package and exits with status 1; fields such as `action` are not included on that line.

Inside a request, prefer `log.Inject` for facts about the request (they appear once, on the access log line) and `log.Extract(ctx)` for separate events.

## Other languages

Services in other languages should log the same shape so dashboards work across a system. A Python example using `python-json-logger` is in [examples/logging/python/json_logging.py](../../examples/logging/python/json_logging.py).
