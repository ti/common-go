package main

import "github.com/ti/common-go/log"

func main() {
	log.With(map[string]any{
		"action": "test",
		"key1":   "val1",
	}).Info("test")
}
