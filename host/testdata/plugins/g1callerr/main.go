package main

import "errors"

func ABIMetadata() string {
	return `{"schema":1,"id":"errmod","version":"1","generation":2,"requires":[],"entries":[{"name":"Fail","kind":"call"}]}`
}
func Fail(in map[string]any) (map[string]any, error) {
	return nil, errors.New("entry failure")
}
func main() {}
