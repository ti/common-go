package stacktrace

import (
	"fmt"
	"runtime"
	"strings"
)

// Callers returns a string representation of the current stacktrace.
func Callers(skip int) (result string) {
	var pcs []uintptr
	const size = 32
	pcs = make([]uintptr, size)
	n := runtime.Callers(skip+1, pcs)
	if n == 0 {
		return
	}
	pcs = pcs[:n]
	frames := runtime.CallersFrames(pcs)
	frame, _ := frames.Next()
	return fmt.Sprintf("%s:%d %s", getLastNPathElements(frame.File, 3), frame.Line, getLastNPathElements(frame.Function, 1))
}

func getLastNPathElements(path string, n int) string {
	var subPath string
	// 使用循环获取最后 n 个子路径
	for i := 0; i < n; i++ {
		if len(path) < 2 {
			return subPath
		}
		index := strings.LastIndex(path[:len(path)-1], "/")
		if index == -1 {
			if i == 0 {
				return path
			}
			return path[index+1:] + "/" + subPath
		}
		if subPath == "" {
			subPath = path[index+1:]
		} else {
			subPath = path[index+1:] + "/" + subPath
		}
		path = path[:index]
	}
	return subPath
}
