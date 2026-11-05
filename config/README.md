# config

Binds a configuration file or folder to a struct and keeps it updated when the source changes (via [objectbind](https://github.com/ti/objectbind)). It also initializes dependency structs declared in the config and applies `Log.Level`.

```go
var cfg Config
err := config.Init(ctx, "", &cfg, dependencies.WithNewFns(database.New))
```

An empty URI falls back to `$CONFIG_PATH`, then the `-c` flag (default `configs/config.yaml`). See [docs/guides/configuration.md](../docs/guides/configuration.md).
