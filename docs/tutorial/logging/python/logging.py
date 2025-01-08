## https://anaconda.org/conda-forge/python-json-logger/

## 初始化日志相关内容
from pythonjsonlogger import jsonlogger
from datetime import datetime
import logging
import tzlocal

localzone = tzlocal.get_localzone()

logger = logging.getLogger()

logHandler = logging.StreamHandler()
class CustomJsonFormatter(jsonlogger.JsonFormatter):
    def add_fields(self, log_record, record, message_dict):
        super(CustomJsonFormatter, self).add_fields(log_record, record, message_dict)
        if not log_record.get('time'):
            log_record['time'] = datetime.now(localzone).isoformat()
        if log_record.get('level'):
            log_record['level'] = log_record['level']
        else:
            log_record['level'] = record.levelname

formatter = CustomJsonFormatter('%(time)s %(level)s %(msg)s %(filename)s %(lineno)s')

logHandler.setFormatter(formatter)
logger.addHandler(logHandler)
logger.setLevel(logging.DEBUG)


### 日志使用
logging.info("Something happened.")
logging.debug("Something happened debug.", extra={"action":"test", "size": 2})
