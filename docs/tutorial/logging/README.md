# 云原生日志格式

为了便于ELK相关日志链路的追踪和可视化管理，服务的日志统一为json格式，日志格式如下：



```json
{"level":"info","time":"2024-01-10T11:19:16.807952+08:00","msg":"test","action":"test","request_id":"uuid","key1":"value1","key2":"value2"}
```

其中 action 为行为记录，request_id是这条日志所关联的来源ID, 通常为http请求头部的`x-request-id`参数，其他的为动态扩展的字段，所有action 不为空的日志都会被自动记录在es日志服务中，并可以通过grafana等工具查看和跟踪。 python和golang等例子如代码所示。
