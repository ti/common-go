# JSON format

HTTP responses (REST, ConnectRPC JSON, auto-HTTP, error bodies) use **snake_case** field names by default, the proto field names:

```json
{"user": {"user_id": "42", "display_name": "Ada", "created_at": "2026-01-31T10:00:00Z"}}
```

Switch the whole server to **camelCase** (the proto JSON names) with either:

```yaml
apis:
  useCamelCase: true
```

```go
gs := grpcmux.NewServer(grpcmux.WithUseCamelCase())
```

```json
{"user": {"userId": "42", "displayName": "Ada", "createdAt": "2026-01-31T10:00:00Z"}}
```

| Proto field | snake_case (default) | camelCase |
|---|---|---|
| `user_id` | `user_id` | `userId` |
| `created_at` | `created_at` | `createdAt` |
| `is_active` | `is_active` | `isActive` |
| `page_size` | `page_size` | `pageSize` |

What the setting covers:

| | Affected |
|---|---|
| REST (grpc-gateway) responses | yes |
| REST error bodies: `error`, `error_code` / `errorCode`, `error_description` / `errorDescription`, `details` | yes |
| ConnectRPC JSON responses | yes |
| Connect error bodies (`code`, `message`, `details`) | no, these names are fixed by the Connect protocol |
| Request bodies | no: input accepts both styles, and unknown fields are ignored |
| Native gRPC and binary Connect (`application/proto`) | no, they are not JSON |

Other output rules (both styles): fields with zero values are omitted; 64-bit integers are strings; enums are their names; `google.protobuf.Timestamp` is RFC 3339; wrapper types (`google.protobuf.Int32Value`, ...) are plain values or omitted.

Pick one style per API and keep it: a browser or mobile frontend usually wants camelCase (it matches connect-web generated types), while snake_case matches the proto files and Python clients. Changing it later is a breaking API change.

## Caller-owned types are encoded with `encoding/json`

The JSON settings above apply to proto messages. Where common-go encodes or decodes *your* Go types (request/response bodies of `dependencies/http`, `mqlru` values, SQL JSON columns, Mongo page results), it uses the standard `encoding/json` (v1) semantics: struct tags decide names, names are matched case-insensitively when decoding, and nil slices are `null`.
