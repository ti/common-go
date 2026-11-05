"""JSON logging for Python services, compatible with the common-go log format.

Every line is one JSON object with the same keys Go services emit, so one
log pipeline (Loki, ELK, CloudWatch, ...) can index both:

    {"time": "...", "level": "INFO", "msg": "...", "action": "CreateUser", "request_id": "..."}

- action: what happened (low cardinality, e.g. the RPC "/pkg.Service/Method").
- request_id: the incoming X-Request-Id header; forward it on outgoing calls.
- any other keys: passed with extra={...}.

Requires: pip install python-json-logger
"""

import logging
import sys
from datetime import datetime, timezone

from pythonjsonlogger.json import JsonFormatter


class CommonGoFormatter(JsonFormatter):
    """Renames fields to match common-go: time, level, msg."""

    def add_fields(self, log_record, record, message_dict):
        super().add_fields(log_record, record, message_dict)
        log_record["time"] = datetime.now(timezone.utc).isoformat()
        log_record["level"] = record.levelname
        log_record["msg"] = log_record.pop("message", record.getMessage())


def setup(level: int = logging.INFO) -> logging.Logger:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(CommonGoFormatter())
    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(level)
    return root


if __name__ == "__main__":
    log = setup(logging.DEBUG)
    log.info("service started", extra={"action": "Start"})
    log.debug("cache warmed", extra={"action": "WarmCache", "size": 2})
    log.warning("slow request", extra={"action": "/example.user.v1.UserService/GetUser",
                                       "request_id": "8a6c2f9e", "duration": 1.2})
