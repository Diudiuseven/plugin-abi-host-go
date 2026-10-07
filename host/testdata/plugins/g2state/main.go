package main

var calls int // module-private global state, separate from g1state's

func ABIMetadata() string {
	return `{"schema":1,"id":"stateful","version":"2","generation":2,"requires":[],"entries":[{"name":"Count","kind":"call"}]}`
}

func Count(in map[string]any) (map[string]any, error) {
	calls++
	return map[string]any{"gen": 2, "calls": calls}, nil
}

func main() {}
