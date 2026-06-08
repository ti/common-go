# Cloud-Native Logging Format

To facilitate ELK-based log tracing and visual management, service logs are unified in JSON format as follows:

```json
{"level":"info","time":"2024-01-10T11:19:16.807952+08:00","msg":"test","action":"test","request_id":"uuid","key1":"value1","key2":"value2"}
```

The `action` field records the behavior, and `request_id` is the source ID associated with the log entry, typically the `x-request-id` parameter from the HTTP request header. Other fields are dynamically extended. All logs with a non-empty `action` are automatically recorded in the ES log service and can be viewed and traced using tools such as Grafana. Examples for Python and Go are shown in the code.
